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

func (r *cliReporter) printf(format string, args ...any) {
	r.logger.Print(r.redactor.Replace(r.urlRedactor.Replace(fmt.Sprintf(format, args...))))
}

func (r *cliReporter) debugf(format string, args ...any) {
	if r.debug {
		r.printf("【调试】"+format, args...)
	}
}

func (r *cliReporter) once(key, message string) {
	if r.messages[key] != message {
		r.messages[key] = message
		r.printf("%s", message)
	}
}

// Count repeated events, not elapsed time or snapshot reads. A changed cause is
// reported immediately; identical failures are summarized every five events.
func (r *cliReporter) problem(key, message string) {
	p := r.problems[key]
	if p.message != message {
		p = cliProblem{message: message}
	}
	p.count++
	r.problems[key] = p
	delete(r.messages, key)
	if p.count == 1 {
		r.printf("%s", message)
	} else if p.count%5 == 0 {
		r.printf("%s（同类问题累计 %d 次）", message, p.count)
	}
}

func (r *cliReporter) startup() {
	c := r.request.Config
	mode := "仅 IPv6 直连"
	if c.Transport.Preferred == core.PreferredICE {
		mode = "优先直连，必要时自动中继"
		if c.TURN.Mode == "off" {
			mode = "优先直连，本机中继已关闭"
		}
		if c.ICE.RelayOnly {
			mode = "仅使用中继"
		}
	}
	r.printf("【启动】hole · 设备「%s」 · 版本 %s", logText(c.DeviceName), logText(core.CoreVersion))
	r.printf("【配置】%s；共享 %d 项服务，访问 %d 项服务", mode, len(c.Provide), len(c.Consume))
	for _, p := range c.Provide {
		r.printf("【共享】「%s」目标服务 %s（%s）", logText(p.ID), logEndpoint(p.Service.Addr, p.Service.Port), strings.ToUpper(p.Service.Protocol))
	}
	for _, c := range c.Consume {
		r.printf("【访问】「%s」本地入口 %s（等待映射就绪）", logText(c.ID), logEndpoint(c.Expose.Addr, c.Expose.Port))
	}
	r.debugf("协调服务器 %s · 会话保留 %s · 兼容旧 IPv6 协议：%t", logServer(r.request.ServerURL), c.SessionTimeout, c.Transport.AllowLegacy)
	if c.Transport.Preferred == core.PreferredICE {
		r.debugf("STUN=%q · 直连探测 %s · 候选收集 %s · 连通检查 %s", c.ICE.STUNURLs, c.ICE.DirectProbeTimeout.Duration(), c.ICE.GatherTimeout.Duration(), c.ICE.ConnectivityTimeout.Duration())
		turn := map[string]string{"worker": "按需申请", "manual": "手动配置", "off": "关闭"}[c.TURN.Mode]
		r.debugf("中继：%s · 凭据有效期 %s · 已配置 %d 个地址", turn, c.TURN.TTL.Duration(), len(c.TURN.URLs))
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
			r.once("engine", "【停止】hole 已停止")
		case "error":
			r.stopped, r.failureReported = true, true
			r.once("engine", "【失败】hole 已停止："+logFault(e.Error))
		case "waiting_network":
			r.once("engine", "【等待】当前没有可用网络，等待网络恢复")
		}
	case "network":
		switch e.State {
		case "changed", "recovering":
			r.once("network", "【网络】检测到网络变化，正在重新检查连接")
		case "waiting_network":
			r.once("network", "【等待】当前没有可用网络，等待网络恢复")
		}
	case "turn":
		if e.Error != nil {
			r.problem("turn", "【等待】中继暂不可用："+logFault(e.Error)+"；稍后自动重试")
		} else if e.State == "ready" {
			if _, failed := r.problems["turn"]; failed {
				r.printf("【恢复】中继连接信息已获取，可以继续尝试中继")
			}
			delete(r.problems, "turn")
		}
	case "config":
		if e.Error != nil {
			r.problem("config", "【失败】配置未生效："+logFault(e.Error))
		} else if e.State == "applied" {
			r.once("config", "【配置】新配置已生效")
		}
	}
}

func (r *cliReporter) signal(e core.Event) {
	previous := r.signalState
	r.signalState = e.State
	switch e.State {
	case "connecting":
		if !r.signalRecovering {
			r.once("signal", "【启动】正在连接协调服务器")
		}
	case "joined":
		if previous == "joined" {
			return
		}
		message := "【连接】已加入房间"
		if r.signalRecovering && r.signalJoined {
			message = "【恢复】协调服务器连接已恢复，已重新加入房间"
		}
		r.once("signal", message)
		r.signalJoined, r.signalRecovering = true, false
		delete(r.problems, "signal")
	case "reconnecting", "error":
		r.signalRecovering = true
		message := "【重试】协调服务器暂未连通"
		if r.signalJoined {
			message = "【重连】协调服务器连接中断"
		}
		if e.Error != nil {
			message += "：" + logFault(e.Error)
		}
		r.problem("signal", message+"；正在重新连接")
	}
}

func (r *cliReporter) mapping(e core.Event) {
	e = r.mappingContext(e)
	previous := r.mappingFacts[e.MappingID]
	key := "mapping/" + e.MappingID
	name := "「" + logText(e.MappingID) + "」"
	var endpoint, role string
	for _, c := range r.request.Config.Consume {
		if c.ID == e.MappingID {
			endpoint, role = logEndpoint(c.Expose.Addr, c.Expose.Port), "consume"
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
		message = "【就绪】" + name + "映射已就绪"
		if r.mappingReady[e.MappingID] {
			message = "【恢复】" + name + "映射已恢复"
		}
		r.mappingReady[e.MappingID] = true
		if role == "consume" {
			message += "，本地入口 " + endpoint
			if e.Peer != "" {
				message += " → 「" + logText(e.Peer) + "」"
			}
		} else if e.Peer != "" {
			message += "，对端「" + logText(e.Peer) + "」"
		}
		if e.Protocol != "" {
			message += "（" + strings.ToUpper(logText(e.Protocol)) + "）"
		}
	case "waiting_peer", "paused":
		message = "【等待】" + name + "等待对端上线"
		if role == "consume" {
			message = "【等待】" + name + "等待服务提供方上线"
		} else if role == "provide" {
			message = "【等待】" + name + "等待访问方上线"
		}
	case "connecting", "reconnecting", "recovering":
		if previous.State == "active" || previous.State == "ready" {
			delete(r.messages, key)
		}
		// ICE progress is shared by a device pair, not repeated for every mapping.
		if e.Profile != core.ProfileICE {
			message = "【连接】正在建立" + name + "映射"
		}
	case "error":
		message = "【失败】" + name + "映射未就绪：" + logFault(e.Error)
	case "closed", "stopped":
		message = "【关闭】" + name + "映射已关闭"
	}
	if message != "" {
		r.once(key, message)
	}
}

func (r *cliReporter) session(e core.Event) {
	key := "session/" + e.MappingID + "/" + e.Peer
	name := "「" + logText(e.MappingID) + "」"
	if e.Peer != "" {
		name += "（对端「" + logText(e.Peer) + "」）"
	}
	if e.Error != nil {
		message := "【失败】" + name + "：" + logSessionFault(e)
		if e.State == "recovering" || e.Error.Code == "session_transport_interrupted" {
			message = "【恢复】" + name + "会话传输中断，正在尝试续接"
		}
		r.problem(key, message)
		return
	}
	if e.State != "active" {
		return
	}
	_, failed := r.problems[key]
	if !failed && !e.Resume {
		return
	}
	message := "【恢复】" + name + "目标服务连接已成功"
	if e.Resume {
		// This event proves one session resumed, not all sessions in the mapping.
		message = "【恢复】" + name + "会话已续接"
	}
	r.once(key, message)
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
	key, name := "peer/"+id, "「"+logText(p.PeerID)+"」"
	phase, path := logPhase(p.Phase), logPath(p)
	message := ""
	switch p.State {
	case "active":
		pathKey := path + "/" + p.AddressFamily
		if !progress.connected || progress.interrupted {
			message = "【连通】与" + name + "连接成功"
			if progress.interrupted {
				message = "【恢复】与" + name + "的连接已恢复"
			}
		} else if progress.path != pathKey {
			message = "【切换】与" + name + "的连接已切换"
		} else if previous.State == "switching" {
			if previous.Generation != p.Generation {
				message = "【切换】与" + name + "的连接切换完成"
			} else {
				message = "【连接】与" + name + "继续使用原连接"
			}
		}
		if message != "" {
			details := []string{path}
			if p.AddressFamily != "" {
				details = append(details, logText(p.AddressFamily))
			}
			if p.ConnectMS > 0 && !(previous.State == "switching" && previous.Generation == p.Generation) {
				details = append(details, fmt.Sprintf("建连耗时 %d 毫秒", p.ConnectMS))
			}
			if len(details) > 0 {
				message += "（" + strings.Join(details, "，") + "）"
			}
		}
		progress.connected, progress.interrupted, progress.path = true, false, pathKey
		progress.attempts = map[string]bool{}
		progress.retryBase, progress.reported = p.RetryCount, p.RetryCount
		delete(r.messages, "network")
	case "switching":
		pending := "新路径"
		if p.PendingPhase != "" {
			pending = logPhase(p.PendingPhase)
		}
		message = "【切换】正在为" + name + "建立新连接（" + pending + "），当前连接继续使用（" + path + "）"
	case "paused", "waiting_peer":
		message = "【等待】对端" + name + "已离线，等待重新上线"
		if previous.State != p.State {
			progress.interrupted = progress.connected
			progress.attempts = map[string]bool{}
		}
	case "error":
		message = "【失败】与" + name + "建立连接失败：" + logFault(p.Error)
		progress.interrupted = progress.connected
	case "closed":
		message = "【关闭】与" + name + "的连接已关闭"
	default:
		if progress.connected && !progress.interrupted {
			progress.interrupted = true
			progress.attempts = map[string]bool{}
			r.once(key, "【重连】与"+name+"的连接中断，正在重新连接")
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
			message = "【连接】正在尝试连接对端" + name + "（" + phase + "）"
		case "waiting_credentials":
			message = "【等待】正在为" + name + "准备中继连接（" + phase + "），等待中继连接信息"
		case "reconnecting", "retrying":
			attempt = "retrying"
			if !progress.interrupted {
				message = "【重试】与" + name + "暂未连通，正在寻找可用路径"
			}
		}
		if progress.attempts[attempt] {
			message = ""
		} else if message != "" {
			progress.attempts[attempt] = true
		}
		if p.RetryCount >= progress.reported+5 {
			message = fmt.Sprintf("【重试】与%s仍未连通，累计发起 %d 次重试", name, p.RetryCount-progress.retryBase)
			progress.reported = p.RetryCount
		}
	}
	if message != "" {
		r.once(key, message)
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
			r.printf("【关闭】与「%s」的连接已关闭", logText(r.peers[id].PeerID))
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
		r.printf("【日志】省略了 %d 条事件（累计 %d），当前连接状态已重新核对", s.EventsDropped-r.dropped, s.EventsDropped)
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
