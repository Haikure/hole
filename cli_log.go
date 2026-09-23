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
	"time"

	"hole/core"
)

type cliProblem struct {
	message string
	count   uint64
}

type cliPeerProgress struct {
	connected   bool
	interrupted bool
	path        string
	attempts    map[string]bool
	retryBase   uint64
	reported    uint64
}

// Presentation is event-driven. Internal generations, leases and statistics do
// not constitute user-visible progress; the latest snapshot supplies path facts.
type cliReporter struct {
	logger           *log.Logger
	request          core.Request
	debug            bool
	redactor         *strings.Replacer
	urlRedactor      *strings.Replacer
	peers            map[string]core.PeerTransportSnapshot
	progress         map[string]*cliPeerProgress
	mappingFacts     map[string]core.Event
	mappingReady     map[string]bool
	messages         map[string]string
	problems         map[string]cliProblem
	signalJoined     bool
	signalRecovering bool
	signalState      string
	stopped          bool
	failureReported  bool
	dropped          uint64
}

func newCLIReporter(logger *log.Logger, request core.Request) *cliReporter {
	secrets := map[string]bool{}
	for _, secret := range []string{request.Config.Password, request.Config.Token, request.Config.TURN.Username, request.Config.TURN.Credential} {
		if secret != "" {
			for _, spelling := range logSpellings(secret) {
				secrets[spelling] = true
			}
		}
	}
	keys := make([]string, 0, len(secrets))
	for secret := range secrets {
		keys = append(keys, secret)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	pairs := make([]string, 0, len(keys)*2)
	for _, secret := range keys {
		pairs = append(pairs, secret, "[redacted]")
	}
	urlPairs := []string{}
	if request.ServerURL != "" {
		clean := logServer(request.ServerURL)
		originals, replacements := logSpellings(request.ServerURL), logSpellings(clean)
		for i, original := range originals {
			urlPairs = append(urlPairs, original, replacements[i])
		}
	}
	return &cliReporter{
		logger: logger, request: request, redactor: strings.NewReplacer(pairs...), urlRedactor: strings.NewReplacer(urlPairs...),
		peers: map[string]core.PeerTransportSnapshot{}, progress: map[string]*cliPeerProgress{},
		mappingFacts: map[string]core.Event{}, mappingReady: map[string]bool{},
		messages: map[string]string{}, problems: map[string]cliProblem{},
	}
}

func logSpellings(value string) []string {
	quoted := strconv.Quote(value)
	return []string{quoted[1 : len(quoted)-1], url.QueryEscape(value), url.PathEscape(value), value}
}

func (r *cliReporter) printf(level, format string, args ...any) {
	r.logger.Print(level + r.redactor.Replace(r.urlRedactor.Replace(fmt.Sprintf(format, args...))))
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
	if p.count == 1 {
		r.printf(level, "%s", message)
	} else if p.count%5 == 0 {
		r.printf(level, "%s x%d", message, p.count)
	}
}

func (r *cliReporter) startup() {
	c := r.request.Config
	r.info("hole %s 设备 %s", logText(core.CoreVersion), logIdent(c.DeviceName))
	for _, p := range c.Provide {
		r.info("%s: 共享 %s://%s", logIdent(p.ID), logText(p.Service.Protocol), logEndpoint(p.Service.Addr, p.Service.Port))
	}
	for _, consume := range c.Consume {
		r.info("%s: 本地入口 %s", logIdent(consume.ID), logEndpoint(consume.Expose.Addr, consume.Expose.Port))
	}
	transport := "ipv6"
	if c.Transport.Preferred == core.PreferredICE {
		transport = "ice"
	}
	r.debugf("config server=%s transport=%s allow_legacy=%t session_timeout=%s", logServer(r.request.ServerURL), transport, c.Transport.AllowLegacy, logDuration(int64(c.SessionTimeout/time.Millisecond)))
	if c.Transport.Preferred == core.PreferredICE {
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
	if e.Kind == "mapping" || e.Kind == "session" {
		e = r.mappingContext(e)
	}
	r.debugEvent(e)
	switch e.Kind {
	case "signal":
		r.signal(e)
	case "mapping":
		r.mapping(e)
	case "session":
		r.session(e)
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
	case "config":
		if e.Error != nil {
			r.problem("config", logLevelError, "配置未生效 ("+logFault(e.Error)+")")
		} else if e.State == "applied" {
			r.once("config", logLevelInfo, "新配置已生效")
		}
	}
}

func (r *cliReporter) signal(e core.Event) {
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
		delete(r.problems, "signal")
	case "reconnecting", "error":
		r.signalRecovering = true
		cause := ""
		if e.Error != nil {
			cause = " (" + logFault(e.Error) + ")"
		}
		if r.signalJoined {
			r.problem("signal", logLevelWarn, "信令连接中断"+cause+"，重连中")
		} else {
			r.problem("signal", logLevelWarn, "信令服务器未连通"+cause+"，重试中")
		}
	}
}

func (r *cliReporter) mapping(e core.Event) {
	e = r.mappingContext(e)
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
		message = "等待对端上线"
		if role == "consume" {
			message = "等待提供方上线"
		} else if role == "provide" {
			message = "等待访问方上线"
		}
	case "connecting", "reconnecting", "recovering":
		if previous.State == "active" || previous.State == "ready" {
			delete(r.messages, key)
		}
		// ICE progress is shared by a device pair, not repeated for every mapping.
		if e.Profile != core.ProfileICE {
			message = "建立映射中"
		}
	case "error":
		r.once(key, logLevelError, subject+"映射未就绪 ("+logFault(e.Error)+")")
		return
	case "closed", "stopped":
		message = "映射已关闭"
	}
	if message != "" {
		r.once(key, logLevelInfo, subject+message)
	}
}

func (r *cliReporter) session(e core.Event) {
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
	if !failed && !e.Resume {
		return
	}
	message := "目标服务已恢复"
	if e.Resume {
		// This event proves one session resumed, not all sessions in the mapping.
		message = "会话已续接"
	}
	r.once(key, logLevelInfo, subject+message+peer)
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

func (r *cliReporter) peer(p core.PeerTransportSnapshot) {
	p = visiblePeer(p)
	id := peerKey(p)
	previous, exists := r.peers[id]
	state := peerLogState(p)
	r.peers[id] = state
	if !exists || !reflect.DeepEqual(previous, state) {
		r.debugPeer(p)
	}
	progress := r.progress[id]
	if progress == nil {
		progress = &cliPeerProgress{attempts: map[string]bool{}}
		r.progress[id] = progress
	}
	key := "peer/" + id
	phase, path := logPhase(p.Phase), logRoute(p)
	if path == "" && p.State == "active" {
		path = "已建立的路径"
	}
	subject := logIdent(p.PeerID) + ": "
	message := ""
	level := logLevelInfo
	switch p.State {
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
			}
		}
		if message != "" && p.ConnectMS > 0 && !(previous.State == "switching" && previous.Generation == p.Generation) {
			message += " " + logDuration(int64(p.ConnectMS))
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
	case "paused", "waiting_peer":
		message = "对端离线，等待上线"
		level = logLevelWarn
		if previous.State != p.State {
			progress.interrupted = progress.connected
			progress.attempts = map[string]bool{}
		}
	case "error":
		message = "连接失败 (" + logFault(p.Error) + ")"
		level = logLevelError
		progress.interrupted = progress.connected
	case "closed":
		message = "连接已关闭"
	default:
		if progress.connected && !progress.interrupted {
			progress.interrupted = true
			progress.attempts = map[string]bool{}
			r.once(key, logLevelWarn, subject+"连接中断，重连中")
			for sessionKey := range r.messages {
				if strings.HasPrefix(sessionKey, "session/") && strings.HasSuffix(sessionKey, "/"+p.PeerID) {
					delete(r.messages, sessionKey)
				}
			}
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
	}
	if message != "" {
		r.once(key, level, subject+message)
	}
}

func (r *cliReporter) snapshot(s core.Snapshot) {
	if !s.Configured {
		return
	}
	if !r.stopped && s.EngineState != "stopped" && s.EngineState != "error" {
		peers := append([]core.PeerTransportSnapshot(nil), s.PeerTransports...)
		sort.Slice(peers, func(i, j int) bool { return peerKey(peers[i]) < peerKey(peers[j]) })
		next := map[string]bool{}
		for _, p := range peers {
			next[peerKey(p)] = true
			r.peer(p)
		}
		closed := []string{}
		for id := range r.peers {
			if !next[id] {
				closed = append(closed, id)
			}
		}
		sort.Strings(closed)
		for _, id := range closed {
			r.info("%s 连接已关闭", logIdent(r.peers[id].PeerID))
			delete(r.peers, id)
			delete(r.progress, id)
			delete(r.messages, "peer/"+id)
		}
		for _, m := range s.Mappings {
			if m.State == "waiting_peer" && s.SignalState != "joined" {
				continue
			}
			e := core.Event{Kind: "mapping", MappingID: m.ID, State: m.State, Protocol: m.Protocol, Peer: m.Peer, Profile: m.Profile, Path: m.Path}
			if m.State == "error" {
				e.Error = m.Error
			}
			r.mapping(e)
			if s.EventsDropped > r.dropped && m.Error != nil && m.State != "error" {
				e.Kind, e.State, e.Error = "session", "error", m.Error
				r.session(e)
			}
		}
	}
	if s.EventsDropped > r.dropped {
		r.warn("省略事件: %d/%d，重新核对", s.EventsDropped-r.dropped, s.EventsDropped)
	}
	r.dropped = s.EventsDropped
}

type cliEventSource interface {
	Events() <-chan core.Event
	Snapshot() core.Snapshot
}

func (r *cliReporter) follow(engine cliEventSource) {
	for e := range engine.Events() {
		if e.Kind == "log" {
			r.debugEvent(e)
			continue
		}
		s := engine.Snapshot()
		if e.Kind != "mapping" && e.Kind != "session" {
			r.event(e)
		}
		// Print the device path before mapping readiness. Snapshots, rather than
		// possibly queued mapping events, decide current readiness.
		r.snapshot(s)
		if e.Kind == "mapping" {
			r.debugEvent(r.mappingContext(e))
		} else if e.Kind == "session" {
			r.event(e)
		}
	}
}
