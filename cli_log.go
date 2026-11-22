package main

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hole/core"
)

func localNATTime(value string) string {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return stamp.Local().Format("2006-01-02 15:04:05")
}

type cliProblem struct {
	message string
	count   uint64
}

type cliSignalFailure struct {
	cause        string
	disconnected bool
	fault        *core.Fault
}

type cliPeerProgress struct {
	connected   bool
	interrupted bool
	path        string
	attempts    map[string]bool
	retryBase   uint64
	reported    uint64
	presence    cliPeerPresence
}

type cliPeerPresence uint8

const (
	cliPresenceLegacy cliPeerPresence = iota
	cliPresenceUnknown
	cliPresenceOnline
	cliPresenceOffline
)

// Presentation is event-driven. Internal generations, leases and statistics do
// not constitute user-visible progress; the latest snapshot supplies path facts.
type cliReporter struct {
	// Serializes events, reloads and logs emitted by library goroutines.
	mu               sync.Mutex
	secrets          map[string]string
	urls             map[string]string
	relayFacts       string
	logger           *log.Logger
	jsonLogger       *log.Logger
	format           string
	context          cliLogContext
	request          core.Request
	debug            bool
	redactor         *strings.Replacer
	urlRedactor      *strings.Replacer
	peers            map[string]core.PeerTransportSnapshot
	progress         map[string]*cliPeerProgress
	mappingFacts     map[string]core.Event
	mappingReady     map[string]bool
	serviceReady     map[string]bool
	listenerFacts    map[string]core.Event
	messages         map[string]string
	problems         map[string]cliProblem
	signalJoined     bool
	signalRecovering bool
	signalState      string
	signalFailure    *cliSignalFailure
	stopped          bool
	failureReported  bool
	dropped          uint64
}

func newCLIReporter(logger *log.Logger, request core.Request) *cliReporter {
	r := &cliReporter{
		logger: logger, request: request, secrets: map[string]string{}, urls: map[string]string{},
		peers: map[string]core.PeerTransportSnapshot{}, progress: map[string]*cliPeerProgress{},
		mappingFacts: map[string]core.Event{}, mappingReady: map[string]bool{},
		serviceReady:  map[string]bool{},
		listenerFacts: map[string]core.Event{},
		messages:      map[string]string{}, problems: map[string]cliProblem{},
	}
	r.protectRequest(request)
	return r
}

// Keep old secrets for DEBUG diagnostics too: previous events may arrive late.
// Call before starting goroutines or with mu held.
func (r *cliReporter) protectRequest(request core.Request) {
	for _, secret := range []string{request.Config.Password, request.Config.Token, request.Config.TURN.Username, request.Config.TURN.Credential} {
		if secret != "" {
			for _, spelling := range logSpellings(secret) {
				r.secrets[spelling] = "[redacted]"
			}
		}
	}
	if request.ServerURL != "" {
		clean := logServer(request.ServerURL)
		originals, replacements := logSpellings(request.ServerURL), logSpellings(clean)
		for i, original := range originals {
			r.urls[original] = replacements[i]
		}
	}
	r.redactor = cliLogReplacer(r.secrets)
	r.urlRedactor = cliLogReplacer(r.urls)
}

func cliLogReplacer(values map[string]string) *strings.Replacer {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	pairs := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		pairs = append(pairs, key, values[key])
	}
	return strings.NewReplacer(pairs...)
}

func logSpellings(value string) []string {
	quoted := strconv.Quote(value)
	return []string{quoted[1 : len(quoted)-1], url.QueryEscape(value), url.PathEscape(value), value}
}

func (r *cliReporter) printf(level, format string, args ...any) {
	r.writeLog(level, fmt.Sprintf(format, args...))
}

func (r *cliReporter) info(format string, args ...any) {
	r.printf(logLevelInfo, format, args...)
}

func (r *cliReporter) warn(format string, args ...any) {
	r.printf(logLevelWarn, format, args...)
}

func (r *cliReporter) errorf(format string, args ...any) {
	r.printf(logLevelError, format, args...)
}

func (r *cliReporter) debugf(format string, args ...any) {
	if r.debug {
		r.printf(logLevelDebug, format, args...)
	}
}

func (r *cliReporter) once(key, level, message string) {
	if r.messages[key] != message {
		r.messages[key] = message
		r.printf(level, "%s", message)
	}
}

// Count repeated events, not elapsed time or snapshot reads. A changed cause is
// reported immediately; identical failures are summarized every five events.
func (r *cliReporter) problem(key, level, message string) {
	p := r.problems[key]
	if p.message != message {
		p = cliProblem{message: message}
	}
	p.count++
	r.problems[key] = p
	delete(r.messages, key)
	c := r.context
	c.Count = p.count
	defer r.logScope(c)()
	if p.count == 1 {
		r.printf(level, "%s", message)
	} else if p.count%5 == 0 {
		r.printf(level, "%s x%d", message, p.count)
	}
}

func (r *cliReporter) startup() {
	defer r.logScope(cliLogContext{Component: "startup"})()
	c := r.request.Config
	r.info("hole %s 设备 %s", logText(core.CoreVersion), logIdent(c.DeviceName))
	r.info("房间 %s，信令 %s", logIdent(c.Room), logServer(r.request.ServerURL))
	policy := "直连优先，失败后尝试中继"
	if c.ICE.RelayOnly {
		policy = "仅中继"
	} else if c.Transport.FixedEndpoint != nil {
		policy = "固定入口直连"
	}
	r.info("连接策略: %s，提供 %d 项服务，使用 %d 项服务", policy, len(c.Provide), len(c.Consume))
	for _, p := range c.Provide {
		r.context.MappingID, r.context.Protocol, r.context.Target = p.ID, p.Service.Protocol, net.JoinHostPort(p.Service.Addr, strconv.Itoa(p.Service.Port))
		r.info("%s: 共享 %s://%s", logIdent(p.ID), logText(p.Service.Protocol), logEndpoint(p.Service.Addr, p.Service.Port))
	}
	for _, consume := range c.Consume {
		r.context.MappingID, r.context.Protocol, r.context.Target = consume.ID, "", net.JoinHostPort(consume.Expose.Addr, strconv.Itoa(consume.Expose.Port))
		r.info("%s: 本地入口 %s", logIdent(consume.ID), logEndpoint(consume.Expose.Addr, consume.Expose.Port))
	}
	r.context = cliLogContext{Component: "config"}
	r.debugf("config server=%s transport=ice session_timeout=%s", logServer(r.request.ServerURL), logDuration(int64(c.SessionTimeout/time.Millisecond)))
	ice := "ice direct_probe=" + logDuration(int64(c.ICE.DirectProbeTimeout.Duration()/time.Millisecond))
	ice += " gather=" + logDuration(int64(c.ICE.GatherTimeout.Duration()/time.Millisecond))
	ice += " connectivity=" + logDuration(int64(c.ICE.ConnectivityTimeout.Duration()/time.Millisecond))
	ice += fmt.Sprintf(" relay_only=%t", c.ICE.RelayOnly)
	if stun := strings.Join(c.ICE.STUNURLs, ","); stun != "" {
		ice += " stun=" + logValue(stun)
	}
	r.debugf("%s", ice)
	r.debugf("turn mode=%s ttl=%s urls=%d", logValue(c.TURN.Mode), logDuration(int64(c.TURN.TTL.Duration()/time.Millisecond)), len(c.TURN.URLs))
}

func logEndpoint(host string, port int) string {
	return logText(net.JoinHostPort(host, strconv.Itoa(port)))
}

func (r *cliReporter) mappingContext(e core.Event) core.Event {
	previous := r.mappingFacts[e.MappingID]
	if e.Protocol == "" {
		e.Protocol = previous.Protocol
	}
	if e.Peer == "" {
		e.Peer = previous.Peer
	}
	if e.Profile == "" {
		e.Profile = previous.Profile
	}
	return e
}

func (r *cliReporter) event(e core.Event) {
	r.eventWithSnapshot(e, core.Snapshot{})
}

func (r *cliReporter) eventWithSnapshot(e core.Event, s core.Snapshot) {
	if e.Kind == "mapping" || e.Kind == "session" {
		e = r.mappingContext(e)
	}
	defer r.logScope(logEventContext(e))()
	r.debugEvent(e)
	switch e.Kind {
	case "signal":
		r.signal(e, s)
	case "mapping":
		r.mapping(e)
	case "session":
		r.session(e)
	case "listener":
		r.listener(e)
	case "engine":
		switch e.State {
		case "stopped", "closed":
			r.stopped = true
			r.once("engine", logLevelInfo, "hole 已停止")
		case "error":
			r.stopped, r.failureReported = true, true
			r.once("engine", logLevelError, "hole 已停止 ("+logFault(e.Error)+")")
		case "waiting_network":
			r.once("engine", logLevelWarn, "没有可用网络，等待恢复")
		}
	case "network":
		switch e.State {
		case "changed", "recovering":
			r.once("network", logLevelWarn, "网络变化，重新检查连接")
		case "waiting_network":
			r.once("network", logLevelWarn, "没有可用网络，等待恢复")
		}
	case "turn":
		if e.Error != nil {
			r.problem("turn", logLevelWarn, "中继不可用 ("+logFault(e.Error)+")")
		} else if e.State == "ready" {
			if _, failed := r.problems["turn"]; failed {
				r.info("中继凭据已获取")
			}
			delete(r.problems, "turn")
		}
	case "relay":
		if e.Error != nil {
			r.problem("relay", logLevelWarn, "Hole 中继服务异常 ("+logFault(e.Error)+")")
		} else if e.State == "changed" {
			if _, failed := r.problems["relay"]; failed {
				r.info("Hole 中继路由已更新")
				delete(r.problems, "relay")
			}
		}
	case "config":
		if e.Error != nil {
			r.problem("config", logLevelError, "配置未生效 ("+logFault(e.Error)+")")
		} else if e.State == "applied" {
			r.once("config", logLevelInfo, "新配置已生效")
		}
	}
}

func (r *cliReporter) signal(e core.Event, s core.Snapshot) {
	previous := r.signalState
	r.signalState = e.State
	switch e.State {
	case "connecting":
		if !r.signalRecovering {
			r.once("signal", logLevelInfo, "连接信令服务器")
		}
	case "joined":
		if previous == "joined" {
			return
		}
		message := "已加入房间"
		if r.signalRecovering && r.signalJoined {
			message = "信令已恢复，重新加入房间"
		}
		r.once("signal", logLevelInfo, message)
		r.signalJoined, r.signalRecovering = true, false
		r.signalFailure = nil
		delete(r.problems, "signal")
		delete(r.messages, "signal-status")
	case "delegated":
		if r.signalFailure == nil || !r.signalFailure.disconnected {
			r.once("signal", logLevelInfo, "信令已委托给房间设备")
			r.signalFailure = nil
			r.signalRecovering = false
			delete(r.problems, "signal")
			delete(r.messages, "signal-status")
		}
		r.signalJoined = true
	case "reconnecting", "error":
		r.signalRecovering = true
		r.signalFailure = &cliSignalFailure{cause: logFault(e.Error), disconnected: r.signalJoined, fault: e.Error}
		r.signalProblem(s, true)
	}
}

// Use current connections and reachable signaling routes, not the Worker's
// possibly stale list of signaling devices in other room components.
func (r *cliReporter) hasSignalDelegate(s core.Snapshot) bool {
	if s.Configured {
		if s.Room != nil {
			return len(s.Room.SignalVia) > 0
		}
		for _, p := range s.PeerTransports {
			if p.State == "active" || p.State == "switching" {
				return true
			}
		}
		return false
	}
	for _, p := range r.peers {
		if p.State == "active" || p.State == "switching" {
			return true
		}
	}
	return false
}

// Keep the cause immediately above its status, including retry summaries.
// Snapshot reconciliation may change severity but never counts as a retry.
func (r *cliReporter) signalProblem(s core.Snapshot, retry bool) {
	failure := r.signalFailure
	if failure == nil {
		return
	}
	defer r.logScope(logEventContext(core.Event{Kind: "signal", State: r.signalState, Error: failure.fault}))()
	level, cause, message := logLevelWarn, "信令连接失败原因: "+failure.cause, "信令服务器未连通，重试中"
	if failure.disconnected {
		cause, message = "信令断开原因: "+failure.cause, "信令连接断开"
		if r.hasSignalDelegate(s) {
			message += " [信令委托]"
		} else {
			level = logLevelError
		}
	}
	key := level + message + "\n" + cause
	if retry {
		p := r.problems["signal"]
		if p.message != key {
			p = cliProblem{message: key}
		}
		p.count++
		r.problems["signal"] = p
		r.context.Count = p.count
		if r.messages["signal-status"] == key && p.count%5 != 0 {
			return
		}
		if p.count > 1 {
			message += fmt.Sprintf(" x%d", p.count)
		}
	} else if r.messages["signal-status"] == key {
		return
	}
	r.messages["signal-status"] = key
	delete(r.messages, "signal")
	r.printf(level, "%s", cause)
	r.printf(level, "%s", message)
}

func (r *cliReporter) mapping(e core.Event) {
	e = r.mappingContext(e)
	defer r.logScope(logEventContext(e))()
	previous := r.mappingFacts[e.MappingID]
	key := "mapping/" + e.MappingID
	subject := logIdent(e.MappingID) + ": "
	var role string
	for _, c := range r.request.Config.Consume {
		if c.ID == e.MappingID {
			role = "consume"
		}
	}
	for _, p := range r.request.Config.Provide {
		if p.ID == e.MappingID {
			role = "provide"
			if e.Protocol == "" {
				e.Protocol = p.Service.Protocol
			}
		}
	}
	r.mappingFacts[e.MappingID] = e
	message := ""
	switch e.State {
	case "active", "ready":
		if previous.State == e.State && previous.Peer == e.Peer && previous.Protocol == e.Protocol {
			return
		}
		if r.mappingReady[e.MappingID] {
			message = "映射已恢复"
		} else {
			message = "映射已就绪"
		}
		r.mappingReady[e.MappingID] = true
		if e.Peer != "" {
			message += "，对端 " + logIdent(e.Peer)
		}
		if e.Protocol != "" {
			message += " (" + strings.ToLower(logText(e.Protocol)) + ")"
		}
	case "waiting_peer", "paused":
		message = "对端离线"
		if role == "consume" {
			message = "提供方离线"
		} else if role == "provide" {
			message = "访问方离线"
		}
	case "unknown":
		message = "对端状态未知"
	case "connecting", "reconnecting", "recovering":
		if previous.State == "active" || previous.State == "ready" {
			delete(r.messages, key)
		}
		// ICE progress is shared by a device pair, not repeated for every mapping.
		if e.Profile != core.ProfileICE && e.Profile != core.ProfileFixed {
			message = "建立映射中"
		}
	case "error":
		if listener, ok := r.listenerFacts[e.MappingID]; ok && listener.State == "error" && e.Error != nil && listener.Error != nil && *e.Error == *listener.Error {
			return
		}
		r.once(key, logLevelError, subject+"映射未就绪 ("+logFault(e.Error)+")")
		return
	case "closed", "stopped":
		message = "映射已关闭"
	}
	if message != "" {
		r.once(key, logLevelInfo, subject+message)
	}
}

func (r *cliReporter) listener(e core.Event) {
	defer r.logScope(logEventContext(e))()
	previous, exists := r.listenerFacts[e.MappingID]
	r.listenerFacts[e.MappingID] = e
	key := "listener/" + e.MappingID
	subject := logIdent(e.MappingID) + ": "
	endpoint := logText(e.Protocol) + "://" + logText(e.Target)
	switch e.State {
	case "ready":
		message := "已监听 " + endpoint
		if exists && previous.State == "error" {
			message = "监听已恢复 " + endpoint
		}
		// Recovery is a single transition; repeated ready snapshots stay quiet.
		if exists && previous.State == "ready" && previous.Protocol == e.Protocol && previous.Target == e.Target {
			return
		}
		r.once(key, logLevelInfo, subject+message)
	case "error":
		cause := logCause(e.Error)
		if cause == "" {
			cause = "监听失败"
		}
		r.once(key, logLevelError, subject+cause+" "+endpoint+" ("+logFault(e.Error)+")")
	case "closed":
		if exists && previous.State == "ready" {
			r.once(key, logLevelInfo, subject+"监听已关闭 "+endpoint)
		}
	}
}

func (r *cliReporter) session(e core.Event) {
	defer r.logScope(logEventContext(e))()
	key := "session/" + e.MappingID + "/" + e.Peer
	subject := logIdent(e.MappingID) + ": "
	peer := ""
	if e.Peer != "" {
		peer = "，对端 " + logIdent(e.Peer)
	}
	if e.Error != nil {
		if e.State == "recovering" || e.Error.Code == "session_transport_interrupted" {
			r.problem(key, logLevelWarn, subject+"会话传输中断，续接中"+peer)
			return
		}
		cause := logCause(e.Error)
		if cause == "" {
			cause = "操作失败"
		}
		if e.Target != "" {
			cause = "目标 " + logText(e.Target) + " " + cause
		}
		r.problem(key, logLevelError, subject+cause+peer+" ("+logFault(e.Error)+")")
		return
	}
	if e.State != "active" {
		return
	}
	_, failed := r.problems[key]
	if !failed && !e.Resume && r.serviceReady[key] {
		return
	}
	message := "目标服务已连通"
	if failed {
		message = "目标服务已恢复"
	}
	if e.Resume {
		// This event proves one session resumed, not all sessions in the mapping.
		message = "会话已续接"
	}
	if e.Target != "" {
		message += "，目标 " + logText(e.Target)
	}
	r.once(key, logLevelInfo, subject+message+peer)
	r.serviceReady[key] = true
	delete(r.problems, key)
}

func visiblePeer(p core.PeerTransportSnapshot) core.PeerTransportSnapshot {
	if p.State != "active" && p.State != "switching" {
		p.PathType, p.AddressFamily, p.RelayProtocol, p.RelaySide = "", "", "", ""
		p.LocalType, p.RemoteType, p.LocalAddress, p.RemoteAddress = "", "", "", ""
		p.ConnectMS, p.ActiveChannels = 0, 0
	}
	return p
}

func peerLogState(p core.PeerTransportSnapshot) core.PeerTransportSnapshot {
	p = visiblePeer(p)
	p.LocalCandidates, p.RemoteCandidates = 0, 0
	p.BytesSent, p.BytesReceived, p.DroppedDatagrams, p.RTTMS, p.LeaseUntil = 0, 0, 0, 0, 0
	p.TURNExpiresAt, p.RelayState = 0, ""
	p.DatagramLimit, p.CandidatePairs = 0, nil
	return p
}

func peerKey(p core.PeerTransportSnapshot) string {
	if p.TransportID != "" {
		return p.TransportID
	}
	return p.PeerID
}

// Room members reflect locally reachable control edges; groups are the last
// Worker-confirmed room-wide view. A group may lag behind a lost local edge.
func (r *cliReporter) peerPresence(s core.Snapshot, peer string) cliPeerPresence {
	if s.Room == nil || peer == "" {
		return cliPresenceLegacy
	}
	for _, member := range s.Room.Members {
		if member.DeviceName == peer {
			return cliPresenceOnline
		}
	}
	if len(s.Room.Groups) == 0 {
		return cliPresenceLegacy
	}
	if s.SignalState != "joined" && (s.SignalState != "delegated" || len(s.Room.SignalVia) == 0) {
		return cliPresenceUnknown
	}
	localGroup, peerGroup, peerDirect := -1, -1, false
	for i, group := range s.Room.Groups {
		for _, device := range group.Members {
			if device == r.request.Config.DeviceName {
				localGroup = i
			}
			if device == peer {
				peerGroup = i
			}
		}
		for _, device := range group.Signaling {
			if device == peer {
				peerDirect = true
			}
		}
	}
	if localGroup < 0 {
		return cliPresenceUnknown
	}
	if peerGroup < 0 {
		return cliPresenceOffline
	}
	if peerGroup != localGroup || peerDirect {
		return cliPresenceOnline
	}
	return cliPresenceUnknown
}

func (r *cliReporter) interruptPeer(progress *cliPeerProgress, peer string) bool {
	if !progress.connected || progress.interrupted {
		return false
	}
	progress.interrupted = true
	progress.attempts = map[string]bool{}
	for sessionKey := range r.messages {
		if strings.HasPrefix(sessionKey, "session/") && strings.HasSuffix(sessionKey, "/"+peer) {
			delete(r.messages, sessionKey)
		}
	}
	return true
}

func (r *cliReporter) peer(p core.PeerTransportSnapshot) {
	r.peerWithPresence(p, cliPresenceLegacy)
}

func (r *cliReporter) peerWithPresence(p core.PeerTransportSnapshot, presence cliPeerPresence) {
	p = visiblePeer(p)
	c := logEventContext(core.Event{Kind: "peer", State: p.State, Peer: p.PeerID, Path: p.PathType, Phase: p.Phase, TransportGeneration: p.Generation, Error: p.Error})
	c.RemoteAddress, c.LocalAddress, c.AddressFamily = p.RemoteAddress, p.LocalAddress, p.AddressFamily
	if p.PathType == "peer_relay" || (p.PathType == "hole" && p.RelayAccessAddress != "") {
		c.RemoteAddress, c.LocalAddress = p.RelayAccessAddress, ""
	}
	c.ElapsedMS, c.RTTMS = p.ConnectMS, p.RTTMS
	defer r.logScope(c)()
	id := peerKey(p)
	previous, exists := r.peers[id]
	state := peerLogState(p)
	r.peers[id] = state
	if !exists || !reflect.DeepEqual(previous, state) {
		r.debugPeer(p)
	}
	if p.LocalNAT.ProbedAt != "" && !reflect.DeepEqual(p.LocalNAT, previous.LocalNAT) {
		r.once("nat/local", logLevelDebug, "本机 NAT: "+logText(core.NATDescription(p.LocalNAT)))
		r.debugf("NAT 探测时间 %s", localNATTime(p.LocalNAT.ProbedAt))
	}
	if p.PeerNAT.ProbedAt != "" && !reflect.DeepEqual(p.PeerNAT, previous.PeerNAT) {
		if r.debug {
			r.once("nat/peer/"+p.PeerID, logLevelDebug, logIdent(p.PeerID)+" 对端 NAT: "+logText(core.NATDescription(p.PeerNAT)))
		}
		r.debugf("对端 NAT 探测时间 %s", localNATTime(p.PeerNAT.ProbedAt))
	}
	progress := r.progress[id]
	if progress == nil {
		progress = &cliPeerProgress{attempts: map[string]bool{}}
		r.progress[id] = progress
	}
	if progress.presence != presence {
		if progress.presence == cliPresenceOffline || progress.presence == cliPresenceUnknown || presence == cliPresenceOffline || presence == cliPresenceUnknown {
			progress.attempts = map[string]bool{}
		}
		progress.presence = presence
	}
	key := "peer/" + id
	phase, path := logPhase(p.Phase), logRoute(p)
	if path == "" && p.State == "active" {
		path = "已建立的路径"
	}
	subject := logIdent(p.PeerID) + ": "
	message := ""
	level := logLevelInfo
	showDuration := true
	displayState := p.State
	if p.State != "active" && p.State != "switching" && p.State != "error" && p.State != "closed" {
		switch presence {
		case cliPresenceOffline:
			displayState = "offline"
		case cliPresenceUnknown:
			displayState = "unknown"
		case cliPresenceOnline:
			if p.State == "paused" || p.State == "waiting_peer" {
				displayState = "waiting"
			}
		}
	}
	r.context.State = displayState
	switch displayState {
	case "active":
		if !progress.connected || progress.interrupted {
			message = "已恢复 " + path
			if !progress.connected {
				message = "已连接 " + path
			}
		} else if progress.path != path {
			message = "已切换 " + path
		} else if previous.State == "switching" && previous.Generation != p.Generation {
			message = "已切换 " + path
		} else if previous.State == "switching" {
			message = "继续使用 " + path
			if p.Error != nil {
				message = "切换失败，继续使用 " + path + " (" + logFault(p.Error) + ")"
				level = logLevelWarn
			}
		} else if logPeerEndpoint(previous) != logPeerEndpoint(p) {
			message = "连接地址已更新 " + path
			showDuration = false
		}
		if message != "" && showDuration && p.ConnectMS > 0 && !(previous.State == "switching" && previous.Generation == p.Generation) {
			message += "，耗时 " + logDuration(int64(p.ConnectMS))
		}
		if message != "" && p.RTTMS > 0 {
			message += "，RTT " + logDuration(p.RTTMS)
		}
		if message != "" {
			if endpoint := logPeerEndpoint(p); endpoint != "" {
				message += "，" + endpoint
			}
		}
		progress.connected, progress.interrupted, progress.path = true, false, path
		progress.attempts = map[string]bool{}
		progress.retryBase, progress.reported = p.RetryCount, p.RetryCount
		delete(r.messages, "network")
	case "switching":
		pending := "新路径"
		if logPhase(p.PendingPhase) != "" {
			pending = logPhase(p.PendingPhase)
		}
		current := path
		if current == "" {
			current = "当前路径"
		}
		message = "尝试切换到 " + pending + " (当前 " + current + ")"
	case "paused", "waiting_peer", "offline":
		message = "离线"
		level = logLevelWarn
		r.interruptPeer(progress, p.PeerID)
		if previous.State != p.State {
			progress.interrupted = progress.connected
			progress.attempts = map[string]bool{}
		}
	case "unknown", "waiting":
		r.interruptPeer(progress, p.PeerID)
		message = "对端状态未知"
		if progress.connected {
			message = "连接中断，对端状态未知"
		}
		if displayState == "waiting" {
			message = "连接中"
			if progress.connected {
				message = "连接中断，恢复中"
			}
		}
		level = logLevelWarn
	case "error":
		message = "连接失败 (" + logFault(p.Error) + ")"
		level = logLevelError
		progress.interrupted = progress.connected
	case "closed":
		message = "连接已关闭"
	default:
		if r.interruptPeer(progress, p.PeerID) {
			r.once(key, logLevelWarn, subject+"连接中断，重连中")
		}
		attempt := p.State + "/" + phase
		switch p.State {
		case "connecting", "checking":
			attempt = "connecting/" + phase
			message = "尝试连接"
			if phase != "" {
				message = "尝试" + phase
			}
		case "waiting_credentials":
			message = "等待中继凭据"
			level = logLevelWarn
		case "reconnecting", "retrying":
			attempt = "retrying"
			message = "重试中"
			level = logLevelWarn
		}
		if p.State == "connecting" && progress.interrupted {
			attempt = "connecting"
		}
		if progress.attempts[attempt] {
			message = ""
		} else if message != "" {
			progress.attempts[attempt] = true
		}
		if p.RetryCount >= progress.reported+5 {
			message = fmt.Sprintf("重试中 (累计 %d)", p.RetryCount-progress.retryBase)
			progress.reported = p.RetryCount
		}
		if (p.State == "retrying" || p.State == "reconnecting") && p.Profile != core.ProfileFixed && p.Error != nil {
			if message == "" && !reflect.DeepEqual(previous.Error, p.Error) {
				message = "重试中"
			}
			if message != "" {
				cause := logCause(p.Error)
				if cause == "" {
					cause = logText(p.Error.Code)
				}
				message += " (" + cause + ")"
			}
		}
	}
	if message != "" {
		if p.Profile == core.ProfileFixed && displayState != "offline" && displayState != "paused" && displayState != "waiting_peer" {
			message += "，固定入口 " + logText(p.FixedEndpoint)
			if p.Error != nil && p.State != "error" && displayState != "offline" && displayState != "unknown" && displayState != "waiting" {
				message += " (" + logFault(p.Error) + ")"
			}
		}
		r.once(key, level, subject+message)
	}
}

func (r *cliReporter) snapshot(s core.Snapshot) {
	if !s.Configured {
		return
	}
	if s.Relay != nil {
		restore := r.logScope(cliLogContext{Component: "relay"})
		facts := fmt.Sprintf("%d/%d/%d", s.Relay.ActivePairs, s.Relay.QueueDrops, s.Relay.RateDrops)
		if facts != r.relayFacts {
			r.relayFacts = facts
			r.debugf("Hole 中继服务: 设备对 %d，转发 %d B，队列丢包 %d，限速丢包 %d，后端 %s", s.Relay.ActivePairs, s.Relay.ForwardedBytes, s.Relay.QueueDrops, s.Relay.RateDrops, logText(s.Relay.Backend))
		}
		restore()
	}
	if !r.stopped && s.EngineState != "stopped" && s.EngineState != "error" {
		peers := append([]core.PeerTransportSnapshot(nil), s.PeerTransports...)
		sort.Slice(peers, func(i, j int) bool { return peerKey(peers[i]) < peerKey(peers[j]) })
		relayAddresses := map[string]string{}
		for _, p := range peers {
			if (p.State == "active" || p.State == "switching") && (p.PathType == "direct" || p.PathType == "fixed") {
				relayAddresses[p.PeerID] = p.RemoteAddress
			}
		}
		next := map[string]bool{}
		for _, p := range peers {
			if p.PathType == "peer_relay" {
				p.RelayAccessAddress = relayAddresses[p.RelayDevice]
			}
			next[peerKey(p)] = true
			r.peerWithPresence(p, r.peerPresence(s, p.PeerID))
		}
		closed := []string{}
		for id := range r.peers {
			if !next[id] {
				closed = append(closed, id)
			}
		}
		sort.Strings(closed)
		for _, id := range closed {
			restore := r.logScope(cliLogContext{Component: "peer", State: "closed", Peer: r.peers[id].PeerID})
			r.info("%s: 连接已关闭", logIdent(r.peers[id].PeerID))
			restore()
			delete(r.peers, id)
			delete(r.progress, id)
			delete(r.messages, "peer/"+id)
		}
		r.listeners(s.Mappings)
		for _, m := range s.Mappings {
			if m.State == "waiting_peer" && s.SignalState != "joined" && s.SignalState != "delegated" {
				continue
			}
			e := core.Event{Kind: "mapping", MappingID: m.ID, State: m.State, Protocol: m.Protocol, Peer: m.Peer, Profile: m.Profile, Path: m.Path}
			if e.State == "waiting_peer" || e.State == "paused" {
				switch r.peerPresence(s, m.Peer) {
				case cliPresenceUnknown:
					e.State = "unknown"
				case cliPresenceOnline:
					e.State = "connecting"
				}
			}
			if m.State == "error" {
				e.Error = m.Error
			}
			r.mapping(e)
			if s.EventsDropped > r.dropped && m.Error != nil && m.State != "error" {
				e.Kind, e.State, e.Error = "session", "error", m.Error
				r.session(e)
			}
		}
		if s.SignalState != "joined" {
			r.signalProblem(s, false)
		}
	}
	if s.EventsDropped > r.dropped {
		defer r.logScope(cliLogContext{Component: "diagnostics", State: "dropped"})()
		r.warn("省略事件: %d/%d，重新核对", s.EventsDropped-r.dropped, s.EventsDropped)
	}
	r.dropped = s.EventsDropped
}

func (r *cliReporter) listeners(mappings []core.MappingSnapshot) {
	seen := make(map[string]bool, len(mappings))
	for _, m := range mappings {
		if m.Listener != nil {
			seen[m.ID] = true
			r.listener(core.Event{Kind: "listener", MappingID: m.ID, State: m.Listener.State, Protocol: m.Listener.Protocol, Target: m.Listener.Address, Error: m.Listener.Error})
		}
	}
	removed := []string{}
	for id := range r.listenerFacts {
		if !seen[id] {
			removed = append(removed, id)
		}
	}
	sort.Strings(removed)
	for _, id := range removed {
		e := r.listenerFacts[id]
		e.State, e.Error = "closed", nil
		r.listener(e)
		delete(r.listenerFacts, id)
		delete(r.messages, "listener/"+id)
	}
}

type cliEventSource interface {
	Events() <-chan core.Event
	Snapshot() core.Snapshot
}

func (r *cliReporter) follow(engine cliEventSource) {
	for e := range engine.Events() {
		// Acquire engine state before the reporter lock: engine operations can
		// themselves log through cliLibraryLog.
		var snapshot core.Snapshot
		if e.Kind != "log" {
			snapshot = engine.Snapshot()
		}
		r.followEvent(e, snapshot)
	}
}

func (r *cliReporter) followEvent(e core.Event, s core.Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.Kind == "log" {
		r.debugEvent(e)
		return
	}
	if e.Kind != "mapping" && e.Kind != "session" && e.Kind != "listener" {
		r.eventWithSnapshot(e, s)
	}
	// Print the device path before mapping readiness. Snapshots, rather than
	// possibly queued mapping events, decide current readiness.
	r.snapshot(s)
	if e.Kind == "mapping" || e.Kind == "listener" {
		r.debugEvent(r.mappingContext(e))
	} else if e.Kind == "session" {
		r.event(e)
	}
}
