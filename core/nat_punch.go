package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"hole/core/natprobe"
)

type natPunchSignal struct {
	Kind           string          `json:"kind"`
	Round          int             `json:"round,omitempty"`
	Trial          int             `json:"trial,omitempty"`
	Result         string          `json:"result,omitempty"`
	Mode           string          `json:"mode,omitempty"`
	Nonce          string          `json:"nonce,omitempty"`
	Snapshot       *NATSnapshot    `json:"snapshot,omitempty"`
	Config         *NATPunchConfig `json:"config,omitempty"`
	Plan           *natPunchPlan   `json:"plan,omitempty"`
	PoolPrediction bool            `json:"pool_prediction,omitempty"`
}

type natPunchPlan struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Target  int    `json:"target"`
	Width   int    `json:"width"`
	Sockets int    `json:"sockets"`
}

type natPunchSession struct {
	local, remote  NATSnapshot
	config         NATPunchConfig
	mode           string
	key            []byte
	poolPrediction bool
	conePoolPlan   *natPunchPlan // selected alternate IP, retaining round-three port
	poolTried      map[string]bool
}

var errNATPunchModel = errors.New("calibration no longer follows allocation model")

const directOrderICEFirst = "ice-first-v1"

// Older Workers cannot coordinate the ICE-to-punch generation change. An
// immediate disabled offer also lets a NAT-first peer proceed with ordinary
// ICE, without waiting for local classification or selecting a punch mode.
func (p *icePeer) offerOrdinaryICE(a *iceAttempt, cfg Config) error {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	config := cfg.ICE.Punch
	config.normalize()
	snapshot := natUnknown()
	return p.sendNATPunch(a, natPunchSignal{Kind: "offer", Mode: "disabled", Nonce: hex.EncodeToString(nonce[:]), Snapshot: &snapshot, Config: &config})
}

func natGroup(s NATSnapshot) string {
	if s.Current().Status != "complete" {
		return "unknown"
	}
	if s.MappingBehavior == "endpoint_independent" {
		return "cone"
	}
	if s.Type == "symmetric" && s.MappingBehavior == "endpoint_dependent" {
		return "symmetric"
	}
	if s.Type == "address_pool" && len(s.IPEvidence) > 1 {
		return "pool"
	}
	return "unknown"
}

func punchMode(a, b NATSnapshot) string {
	ag, bg := natGroup(a), natGroup(b)
	if ag == "cone" && bg == "cone" {
		return "cone"
	}
	predictable := func(s NATSnapshot) bool {
		return s.Predictability == "predictable_incremental" || s.Predictability == "predictable_decremental" || s.Predictability == "incremental" || s.Predictability == "decremental"
	}
	pool := func(s NATSnapshot) bool {
		if len(s.IPEvidence) < 2 || natGroup(s) == "unknown" || natGroup(s) == "cone" {
			return false
		}
		for _, evidence := range s.IPEvidence {
			if evidence.Trend != "irregular" {
				return true
			}
		}
		return false
	}
	if len(a.IPEvidence) > 1 && ag != "cone" || len(b.IPEvidence) > 1 && bg != "cone" {
		eligible := func(s NATSnapshot) bool {
			return natGroup(s) == "cone" || pool(s) || len(s.IPEvidence) < 2 && natGroup(s) == "symmetric" && predictable(s)
		}
		if eligible(a) && eligible(b) {
			return "pool"
		}
		return "ice"
	}
	if ag == "cone" && bg == "symmetric" && predictable(b) || bg == "cone" && ag == "symmetric" && predictable(a) {
		return "single"
	}
	model := func(s NATSnapshot) bool {
		return s.PortStep > 0 && s.PortStep <= 1024 && (s.AllocationScope == "same_socket" || s.AllocationScope == "cross_socket")
	}
	if ag == "symmetric" && bg == "symmetric" && model(a) && model(b) {
		if a.AllocationScope == "cross_socket" && b.AllocationScope == "cross_socket" {
			return "fixed"
		}
		return "next"
	}
	return "ice"
}

func (p *icePeer) sendNATPunch(a *iceAttempt, value natPunchSignal) error {
	// The WebSocket writer encodes asynchronously; never queue pointers to
	// calibration or negotiated configuration that the attempt may update.
	if value.Config != nil {
		copy := *value.Config
		value.Config = &copy
	}
	if value.Snapshot != nil {
		copy := *value.Snapshot
		copy.IPEvidence = append([]natprobe.IPEvidence(nil), copy.IPEvidence...)
		value.Snapshot = &copy
	}
	if value.Plan != nil {
		copy := *value.Plan
		value.Plan = &copy
	}
	m := signalMessage("nat_punch")
	m.TransportID, m.TransportGeneration, m.ToPeerID = a.ready.TransportID, a.ready.TransportGeneration, a.ready.PeerDevice
	m.Punch = &value
	err := p.coordinator.send(m)
	event := NATPunchProcessEvent{Kind: "signal_sent", Title: "提交 " + value.Kind, Direction: "out", Packet: value.Kind, Round: value.Round, Trial: value.Trial, Validation: "queued", Mode: value.Mode, Result: value.Result, Model: value.Snapshot, Plan: value.Plan}
	if err != nil {
		event.Kind, event.Detail, event.Validation = "signal_error", redact(err.Error(), p.coordinator.request().Config), "send_failed"
	}
	a.process.record(event)
	return err
}

// One attempt reader owns negotiation. Keep readiness from a faster peer for
// the next round, so a delayed failure cannot discard its new calibration.
func (p *icePeer) waitNATPunch(ctx context.Context, a *iceAttempt, kind string, round int) (*natPunchSignal, error) {
	return p.waitNATPunchTrial(ctx, a, kind, round, 0)
}

func (p *icePeer) waitNATPunchTrial(ctx context.Context, a *iceAttempt, kind string, round, trial int) (_ *natPunchSignal, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("等待对端 %s 确认（轮次 %d，校准序号 %d）：%w", kind, round, trial, err)
		}
	}()
	for i, m := range a.punchBuffered {
		if m.Kind == "failed" && m.Round == round && kind != "failed" {
			return nil, errors.New("peer round failed")
		}
		if m.Kind == kind && m.Round == round && m.Trial == trial {
			a.punchBuffered = append(a.punchBuffered[:i], a.punchBuffered[i+1:]...)
			return &m, nil
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-a.failed:
			return nil, err
		case m := <-a.events:
			if m.Type == "ice_description" || m.Type == "ice_candidate" {
				return nil, errors.New("unexpected ICE during punch negotiation")
			}
			if m.Punch == nil {
				continue
			}
			a.process.record(NATPunchProcessEvent{Kind: "signal_received", Title: "收到 " + m.Punch.Kind,
				Direction: "in", Packet: m.Punch.Kind, Round: m.Punch.Round, Trial: m.Punch.Trial, Mode: m.Punch.Mode, Result: m.Punch.Result, Model: m.Punch.Snapshot, Plan: m.Punch.Plan})
			if m.Punch.Kind == "abort" {
				return nil, errors.New("peer stopped prediction")
			}
			if m.Punch.Kind == "failed" && m.Punch.Round == round && kind != "failed" {
				// The round owner still needs this result for the failure barrier,
				// including the unreachable evidence required by cone-pool fallback.
				a.punchBuffered = append(a.punchBuffered, *m.Punch)
				return nil, errors.New("peer round failed")
			}
			if m.Punch.Kind == kind && m.Punch.Round == round && m.Punch.Trial == trial {
				return m.Punch, nil
			}
			if m.Punch.Round >= round && (m.Punch.Round != round || m.Punch.Trial == 0 || m.Punch.Trial >= trial) && len(a.punchBuffered) < 64 {
				a.punchBuffered = append(a.punchBuffered, *m.Punch)
			}
		}
	}
}

func (p *icePeer) prepareNATPunch(a *iceAttempt, platform Platform, cfg Config) (_ *natPunchSession, err error) {
	a.processStep("waiting_nat", "等待本机 NAT 采样结果", 0, 0)
	a.process.sampling(p.coordinator.localNATProcess())
	urls := natURLs(cfg.ICE)
	var release func()
	if a.ready.PunchSchedule == natPunchSchedule && len(urls) > 0 {
		// Do not hold one device's allocator while waiting for the peer's
		// offer. Ordered dual-device reservation happens after both offers.
		queueCtx, stopQueue := authorizationContext(a.ctx, a.ready)
		release, err = p.coordinator.natAllocation.acquire(queueCtx)
		stopQueue()
		if err != nil {
			return nil, err
		}
	}
	defer func() {
		if release != nil {
			release()
		}
		if err != nil {
			_ = p.sendNATPunch(a, natPunchSignal{Kind: "abort"})
		}
	}()
	// Queueing has its own lease bound. Start the discovery budget afterwards.
	ctx, cancel := context.WithTimeout(a.ctx, 3*natprobe.DefaultTimeout+2*time.Second)
	defer cancel()
	local := p.coordinator.localNAT()
	if release != nil {
		// Refresh before selecting a strategy, including a recent conclusive
		// irregular classification. Every peer in this generation does the same.
		local = p.probeNATPunchModel(ctx, a, platform, cfg, 0)
		release()
		release = nil
	} else if len(urls) > 0 {
		p.coordinator.requestNATProbeForConnection()
		local = p.coordinator.localNAT()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for local.ProbedAt == "" {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
				local = p.coordinator.localNAT()
				a.process.sampling(p.coordinator.localNATProcess())
			}
		}
	}
	// Local discovery is useful even if the peer's offer never arrives.
	a.process.models(0, "", local, NATSnapshot{})
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	config := cfg.ICE.Punch
	config.normalize()
	encoded := hex.EncodeToString(nonce[:])
	offerMode := ""
	if len(natURLs(cfg.ICE)) == 0 || local.Status == "not_applicable" {
		offerMode = "disabled"
	}
	if err := p.sendNATPunch(a, natPunchSignal{Kind: "offer", Mode: offerMode, Nonce: encoded, Snapshot: &local, Config: &config, PoolPrediction: true}); err != nil {
		return nil, err
	}
	peer, err := p.waitNATPunch(ctx, a, "offer", 0)
	if err != nil {
		return nil, err
	}
	if peer.Snapshot == nil || peer.Config == nil || peer.Config.validate() != nil {
		return nil, errors.New("invalid punch offer")
	}
	if key, e := hex.DecodeString(peer.Nonce); e != nil || len(key) != 32 {
		return nil, errors.New("invalid punch nonce")
	}
	// The smaller time/scan budgets are shared; each side's socket count is
	// carried in its calibrated plan. No room password is sent in UDP packets.
	config.Timeout = min(config.Timeout, peer.Config.Timeout)
	config.RoundTimeout = min(config.RoundTimeout, peer.Config.RoundTimeout)
	config.ScanWidth = min(config.ScanWidth, peer.Config.ScanWidth)
	first, second := encoded, peer.Nonce
	if first > second {
		first, second = second, first
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d/%s/%s", natPunchVersion, a.ready.TransportID, a.ready.TransportGeneration, first, second)))
	s := &natPunchSession{local: local, remote: *peer.Snapshot, config: config, mode: punchMode(local, *peer.Snapshot), key: key[:]}
	s.poolPrediction = peer.PoolPrediction
	if s.mode == "pool" && !s.poolPrediction {
		s.mode = "ice"
	}
	if offerMode == "disabled" || peer.Mode == "disabled" {
		s.mode = "ordinary"
	}
	p.mu.Lock()
	p.stats.PeerNAT = peer.Snapshot.Current()
	p.mu.Unlock()
	p.transportEvent(a, "punch_"+s.mode, nil)
	a.process.models(0, s.mode, s.local, s.remote)
	a.processStep("strategy", "选定本次打洞策略", 0, 0)
	return s, nil
}

func makeNATPunchPlan(mapped netip.AddrPort, model NATSnapshot, mode string, count, width int) (*natPunchPlan, error) {
	if !mapped.IsValid() || !mapped.Addr().Is4() || mapped.Addr().IsUnspecified() || mapped.Addr().IsMulticast() || mapped.Port() == 0 || count < 1 || count > 64 {
		return nil, errors.New("invalid punch calibration")
	}
	plan := &natPunchPlan{IP: mapped.Addr().String(), Port: int(mapped.Port()), Target: int(mapped.Port()), Sockets: count, Width: 1}
	if mode == "pool" {
		return makePoolPunchPlan(plan, model, width)
	}
	if model.PublicIP != "" && plan.IP != model.PublicIP {
		return nil, fmt.Errorf("%w: public IP changed (model=%s, calibrated=%s)", errNATPunchModel, model.PublicIP, plan.IP)
	}
	if mode == "single" && natGroup(model) == "symmetric" {
		plan.Width = width
		plan.Target++
		if model.Predictability == "predictable_decremental" || model.Predictability == "decremental" {
			plan.Target = plan.Port - width
		}
	} else if mode == "fixed" || mode == "next" {
		if plan.IP != model.PublicIP || model.PortStep < 1 || model.PortStep > 1024 || model.AllocationScope != "same_socket" && model.AllocationScope != "cross_socket" {
			return nil, errors.New("invalid allocation model")
		}
		offset := 1
		if mode == "fixed" {
			if model.AllocationScope != "cross_socket" {
				return nil, errors.New("cross socket evidence required")
			}
			offset = max(1, count/2)
		} else if count != 1 {
			return nil, errors.New("next prediction requires one socket")
		}
		plan.Target += model.PortStep * offset
		if model.AllocationScope == "cross_socket" && (plan.Port <= model.LastMappingPort || (plan.Port-model.LastMappingPort)%model.PortStep != 0) {
			return nil, errNATPunchModel
		}
	}
	if plan.Width < 1 || plan.Width > 256 || plan.Target < 1 || plan.Target+plan.Width-1 > 65535 {
		return nil, errors.New("prediction exceeds port boundary")
	}
	return plan, nil
}

func (p *icePeer) predictNATPunch(a *iceAttempt, platform Platform, cfg Config, s *natPunchSession) (_ *liveICETransport, err error) {
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	defer func() {
		if err != nil {
			_ = p.sendNATPunch(a, natPunchSignal{Kind: "abort"})
		}
	}()
	scheduled := a.ready.PunchSchedule == natPunchSchedule
	if scheduled {
		release, e := p.reserveNATPunch(a)
		if e != nil {
			return nil, e
		}
		defer release()
	}
	var held []net.PacketConn
	var selected net.PacketConn
	defer func() {
		for _, pc := range held {
			if pc != selected {
				_ = pc.Close()
				a.process.record(NATPunchProcessEvent{Kind: "socket_closed", Title: "释放未选中的 socket", SocketID: a.process.socketID(pc)})
			}
		}
	}()
	refresh := false
	lastServer := ""
	poolEnabled := a.ready.PunchConePool == natPunchConePool && a.ready.PunchCalibration == natPunchCalibration && scheduled
	poolUnreachable := true
	var poolOriginal *natPunchPlan
	var poolSocket net.PacketConn
	var poolIPs []string
	for round := 1; round <= 3+maxConePoolAttempts; round++ {
		extra := round > 3
		if extra {
			if !poolEnabled || !poolUnreachable || poolOriginal == nil || !conePoolPair(s) {
				reason := "前三轮未全部完成无有效 ACK 的探测"
				if !poolEnabled {
					reason = "双方或 Worker 未协商保留端口换 IP 能力"
				} else if !conePoolPair(s) {
					reason = "双端证据不满足疑似 No-PAT 多出口锥形与对称型配对"
				} else if poolOriginal == nil {
					reason = "第三轮未取得可保留的锥形映射计划"
				}
				a.process.record(NATPunchProcessEvent{Kind: "pool_skipped", Title: "未进入保留端口换 IP 阶段", Round: round, Detail: reason})
				break
			}
			if round == 4 {
				cone := s.local
				if natGroup(cone) != "cone" {
					cone = s.remote
				}
				poolIPs = conePoolCandidates(cone, poolOriginal.IP)
			}
			if round-4 >= len(poolIPs) {
				a.process.record(NATPunchProcessEvent{Kind: "pool_exhausted", Title: "没有尚未尝试的已观测备用公网 IP", Round: round})
				break
			}
		}
		a.processStep("model", "准备本轮 NAT 分配模型", round, 0)
		if scheduled && !extra {
			if e := p.refreshNATPunchModel(a, platform, cfg, s, round, refresh); e != nil {
				return nil, e
			}
		}
		model := s.local
		poolPair := poolEnabled && conePoolPair(s)
		a.process.models(round, s.mode, s.local, s.remote)
		roundUnreachable := false
		executionDeadline := time.Now().Add(s.config.Timeout.Duration())
		if a.ready.PunchCalibration == natPunchCalibration {
			// DNS, Binding retries and bilateral calibrated/GO exchanges share
			// this deadline. Leave bounded room for signaling after a slow STUN
			// result; UDP/QUIC still has its own shorter RoundTimeout below.
			executionDeadline = executionDeadline.Add(natPunchSignalGrace)
		}
		live, roundErr := func() (_ *liveICETransport, roundError error) {
			diagnostic := NATPunchDiagnostic{Generation: a.ready.TransportGeneration, Round: round, Mode: s.mode,
				StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Stage: "calibration"}
			defer func() {
				if roundError != nil {
					diagnostic.Error = redact(roundError.Error(), cfg)
				}
				diagnostic.ReadError = redact(diagnostic.ReadError, cfg)
				diagnostic.UDPReadError = redact(diagnostic.UDPReadError, cfg)
				p.recordPunchDiagnostic(a, diagnostic)
				a.process.diagnostic(diagnostic)
				if a.diagnostics != nil {
					a.diagnostics.log("round_finished", "nat_punch", fmt.Sprintf("round=%d mode=%s stage=%s calibrations=%d sockets=%d probes_sent=%d acks_received=%d acks_verified=%d quic_established=%t error=%q",
						round, diagnostic.Mode, diagnostic.Stage, diagnostic.CalibrationAttempts, diagnostic.Sockets, diagnostic.ProbesSent, diagnostic.ACKsReceived, diagnostic.ACKsVerified, diagnostic.QUICEstablished, diagnostic.Error))
				}
				if roundError != nil {
					a.process.record(NATPunchProcessEvent{Kind: "round_failed", Title: "本轮打洞已结束", Round: round, Detail: diagnostic.Error})
				}
			}()
			ctx, stopAttempt := context.WithDeadline(ctx, executionDeadline)
			defer stopAttempt()
			if extra {
				if err := p.selectConePoolIP(ctx, a, s, round, poolOriginal, poolIPs); err != nil {
					return nil, err
				}
				diagnostic.PoolIP = s.conePoolPlan.IP
				diagnostic.RetainedSocket = natGroup(model) == "cone"
			}
			a.processStep("dns", "解析并选择本轮 IPv4 STUN 服务", round, 0)
			var servers []*net.UDPAddr
			if !extra || natGroup(model) != "cone" {
				var err error
				servers, err = punchSTUNServers(ctx, platform, natURLs(cfg.ICE), lastServer)
				if err != nil {
					return nil, err
				}
			}
			for _, server := range servers {
				a.process.record(NATPunchProcessEvent{Kind: "stun_server", Title: "本轮可用 STUN 端点", Round: round, RemoteAddress: server.String()})
			}
			bindAddress, err := natPunchBind(ctx, platform, cfg.ICE)
			if err != nil {
				return nil, err
			}
			open := func() (net.PacketConn, error) {
				pc, e := platform.ListenPacket(ctx, "udp4", bindAddress)
				if e == nil {
					held = append(held, pc)
					a.process.socket(pc, "business", round, 0)
				}
				return pc, e
			}
			count := 1
			if s.mode == "fixed" {
				count = s.config.DualSockets
				if round >= 2 {
					count = s.config.DualRetrySockets
				}
			}
			if s.mode == "single" && natGroup(model) == "symmetric" || s.mode == "pool" && natGroup(model) != "cone" {
				count = s.config.Sockets
			}
			if s.mode == "pool" && natGroup(s.remote) != "cone" {
				// Keep the total socket/target tasks within the existing scan
				// ceiling, including when both peers need a port window.
				count = min(count, max(1, 256/max(1, s.config.ScanWidth)))
			}
			var sockets []net.PacketConn
			a.processStep("binding", "预绑定本轮业务 socket", round, 0)
			for range count {
				pc := poolSocket
				var e error
				if !extra || natGroup(model) != "cone" {
					pc, e = open()
				}
				if e != nil {
					return nil, e
				}
				if pc == nil {
					return nil, errors.New("retained cone socket missing")
				}
				sockets = append(sockets, pc)
			}
			plans, e := p.calibrateNATPunch(ctx, a, s, round, servers, sockets, open, &lastServer, &diagnostic)
			if e != nil {
				return nil, e
			}
			if extra && natGroup(model) == "cone" {
				diagnostic.MappedAddress = fmt.Sprintf("%s:%d", poolOriginal.IP, poolOriginal.Port)
			}
			diagnostic.LocalAddress = sockets[0].LocalAddr().String()
			diagnostic.Sockets = len(sockets)
			diagnostic.Stage = "coordination"
			a.processStep("coordination", "交换校准结果并校验双方计划", round, diagnostic.CalibrationAttempts)
			plan, peerPlan := plans.local, plans.remote
			trial := 0
			if a.ready.PunchCalibration == natPunchCalibration {
				trial = diagnostic.CalibrationAttempts
			} else {
				if e = p.sendNATPunch(a, natPunchSignal{Kind: "ready", Round: round, Mode: s.mode, Plan: plan}); e != nil {
					return nil, e
				}
				peer, e := p.waitNATPunch(ctx, a, "ready", round)
				if e != nil {
					return nil, e
				}
				if peer.Mode != s.mode || peer.Plan == nil {
					return nil, errors.New("punch plan missing")
				}
				peerPlan = peer.Plan
			}
			remoteIP, e := netip.ParseAddr(peerPlan.IP)
			if e != nil || peerPlan.Port < 1 || peerPlan.Port > 65535 {
				return nil, errors.New("invalid peer calibration")
			}
			var want *natPunchPlan
			if extra && natGroup(s.remote) == "cone" {
				want = s.conePoolPlan
			} else {
				want, e = makeNATPunchPlan(netip.AddrPortFrom(remoteIP, uint16(peerPlan.Port)), s.remote, s.mode, peerPlan.Sockets, s.config.ScanWidth)
			}
			if e != nil {
				return nil, e
			}
			if *want != *peerPlan {
				return nil, errors.New("peer punch plan differs from model")
			}
			if round == 3 && poolEnabled && conePoolPair(s) {
				copy := *peerPlan
				if natGroup(model) == "cone" {
					copy = *plan
					bound, err := netip.ParseAddrPort(sockets[0].LocalAddr().String())
					if err != nil || int(bound.Port()) != plan.Port {
						poolUnreachable = false
						a.process.record(NATPunchProcessEvent{Kind: "pool_skipped", Title: "本轮未保持本地端口，无法保留端口换 IP", Round: round,
							Detail: fmt.Sprintf("绑定 %s，校准公网端口 %d", sockets[0].LocalAddr(), plan.Port)})
					} else {
						poolSocket = sockets[0]
					}
				}
				poolOriginal = &copy
			}
			a.process.plans(round, trial, plan, peerPlan)
			controlling := a.ready.InitiatorID == p.coordinator.deviceID()
			if controlling {
				e = p.sendNATPunch(a, natPunchSignal{Kind: "go", Round: round, Trial: trial})
			} else {
				_, e = p.waitNATPunchTrial(ctx, a, "go", round, trial)
			}
			if e != nil {
				return nil, e
			}
			var targets []*net.UDPAddr
			for port := want.Target; port < want.Target+want.Width; port++ {
				targets = append(targets, &net.UDPAddr{IP: net.ParseIP(want.IP), Port: port})
			}
			if want.Target < want.Port && (s.mode == "pool" || s.mode == "single" &&
				(s.remote.Predictability == "predictable_decremental" || s.remote.Predictability == "decremental")) {
				// Try the nearest predicted decrement first, preserving the
				// allocation order when the two peers have opposite directions.
				for i, j := 0, len(targets)-1; i < j; i, j = i+1, j-1 {
					targets[i], targets[j] = targets[j], targets[i]
				}
			}
			// A cone scans from its retained STUN socket; the symmetric side
			// opens ten fresh mappings to that one exact endpoint (testnat).
			dialer := controlling
			if s.mode == "single" || s.mode == "pool" && (natGroup(model) == "cone" || natGroup(s.remote) == "cone") {
				dialer = natGroup(model) == "cone"
			}
			attempt := newNATPunchAttempt(sockets)
			attempt.process = a.process
			for _, pc := range sockets {
				a.process.socket(pc, "business", round, trial)
			}
			a.process.targets(round, targets, dialer)
			a.processStep("udp", "向本轮目标端口发送 UDP 探测", round, trial)
			diagnostic.Stage, diagnostic.Dialer = "udp", dialer
			diagnostic.TargetFirst, diagnostic.TargetLast = targets[0].String(), targets[len(targets)-1].String()
			attempt.diagnostic = diagnostic
			roundCtx, stopRound := context.WithTimeout(ctx, s.config.RoundTimeout.Duration())
			connection, e := attempt.run(roundCtx, dialer, round, s.key, targets, p.punchTLS(a))
			stopRound()
			diagnostic = attempt.diagnostic
			roundUnreachable = e != nil && diagnostic.ProbesSent > 0 && diagnostic.ACKsReceived == 0 && !diagnostic.QUICDialStarted && !diagnostic.QUICEstablished
			if diagnostic.QUICDialStarted {
				diagnostic.Stage = "quic"
			}
			if e == nil {
				diagnostic.Stage = "mux"
				a.processStep("mux", "QUIC 已建立，协商身份与业务通道", round, trial)
				deadline, _ := ctx.Deadline()
				mux, muxErr := newMuxPeerPrepared(a.ctx, p, connection, a.ready, nil, deadline)
				if muxErr != nil {
					attempt.close()
					return nil, muxErr
				}
				selected = sockets[attempt.selected]
				localAddress, localType := natPunchPathAddress(ctx, platform, connection.LocalAddr(), connection.RemoteAddr(), mux.observedLocalAddress)
				if localType == "prflx" {
					a.process.mapped(selected, localAddress, "peer_observed", round, trial)
				}
				localSocketAddress := ""
				if localType == "host" {
					localSocketAddress = localAddress
				}
				a.process.selected(selected, localSocketAddress, connection.RemoteAddr().String(), round)
				p.mu.Lock()
				p.stats.PathType, p.stats.LocalType, p.stats.RemoteType = "direct", localType, "prflx"
				p.stats.AddressFamily = "IPv4"
				p.stats.LocalAddress, p.stats.RemoteAddress = localAddress, connection.RemoteAddr().String()
				p.mu.Unlock()
				live := &liveICETransport{ready: a.ready, mux: mux, quic: attempt.transports[attempt.selected], session: connection, cancel: a.cancel}
				live.fixedCleanup = func() {
					attempt.close()
					_ = selected.Close()
					a.process.record(NATPunchProcessEvent{Kind: "socket_closed", Title: "业务 socket 已关闭", SocketID: a.process.socketID(selected), Round: round})
				}
				diagnostic.Stage = "connected"
				a.processStep("connected", "直连业务通道已就绪", round, trial)
				return live, nil
			}
			attempt.close()
			for _, pc := range sockets {
				a.process.record(NATPunchProcessEvent{Kind: "socket_retained", Title: "保留本轮端口占用", SocketID: a.process.socketID(pc), Round: round})
			}
			s.local.LastMappingPort, s.remote.LastMappingPort = plan.Port, peerPlan.Port
			return nil, e
		}()
		if roundErr == nil {
			return live, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		refresh = errors.Is(roundErr, errNATPunchModel)
		result := ""
		if poolPair && roundUnreachable {
			result = "unreachable"
		}
		if e := p.sendNATPunch(a, natPunchSignal{Kind: "failed", Round: round, Result: result}); e != nil {
			return nil, e
		}
		if scheduled || poolPair {
			// A fast local failure must not move on while the peer is still
			// executing this round. Its full budget plus signaling grace is
			// independent of our expired DNS/calibration/UDP deadline.
			a.processStep("coordination", "等待双方本轮结束，准备下一轮", round, 0)
			waitCtx, stop := context.WithTimeout(ctx, s.config.Timeout.Duration()+2*natPunchSignalGrace)
			peer, e := p.waitNATPunch(waitCtx, a, "failed", round)
			stop()
			if e != nil {
				return nil, e
			}
			poolUnreachable = poolUnreachable && roundUnreachable && peer.Result == "unreachable"
		}
		if !poolPair {
			poolUnreachable = false
		}
	}
	return nil, errors.New("prediction rounds exhausted")
}

// The peer sees the mapping for this exact QUIC path, unlike a STUN server
// which may receive another endpoint-dependent NAT mapping. The observation
// is optional for older peers and must never affect dialing or authentication.
func natPunchPathAddress(ctx context.Context, platform Platform, local, remote net.Addr, observed string) (string, string) {
	if endpoint, err := netip.ParseAddrPort(observed); err == nil {
		ip := endpoint.Addr().Unmap()
		if ip.Is4() && endpoint.Port() != 0 && !ip.IsUnspecified() && !ip.IsMulticast() && ip != netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return netip.AddrPortFrom(ip, endpoint.Port()).String(), "prflx"
		}
	}
	return natPunchLocalAddress(ctx, platform, local, remote), "host"
}

// A wildcard listener's LocalAddr is a bind address, not a path endpoint. QUIC
// may know the concrete destination from packet info; otherwise query the route
// on the same pinned Platform. UDP connect sends no packet and the temporary
// socket's port must never replace the punched socket's port in telemetry.
func natPunchLocalAddress(ctx context.Context, platform Platform, local, remote net.Addr) string {
	if local == nil {
		return ""
	}
	bound, err := netip.ParseAddrPort(local.String())
	if err != nil || !bound.Addr().Unmap().Is4() || bound.Port() == 0 {
		return ""
	}
	if !bound.Addr().IsUnspecified() {
		return bound.String()
	}
	if remote == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	route, err := platform.DialSignal(ctx, "udp4", remote.String())
	if err != nil {
		return ""
	}
	defer route.Close()
	if route.LocalAddr() == nil {
		return ""
	}
	address, err := netip.ParseAddrPort(route.LocalAddr().String())
	if err != nil || !address.Addr().Unmap().Is4() || address.Addr().IsUnspecified() || address.Addr().IsMulticast() {
		return ""
	}
	return netip.AddrPortFrom(address.Addr().Unmap(), bound.Port()).String()
}

func natPunchBind(ctx context.Context, platform Platform, cfg ICEConfig) (string, error) {
	if len(cfg.InterfaceAllowlist) == 0 {
		return "0.0.0.0:0", nil
	}
	facts, ok := platform.(ICEPlatform)
	if !ok {
		return "", errors.New("network interface information unavailable")
	}
	interfaces, err := facts.InterfaceSnapshots(ctx)
	if err != nil {
		return "", err
	}
	allow := stringSet(cfg.InterfaceAllowlist)
	for _, iface := range interfaces {
		if !iface.Up || !allow[iface.Name] || iface.Loopback && !cfg.IncludeLoopback {
			continue
		}
		for _, raw := range iface.Addresses {
			ip := net.ParseIP(raw)
			if ip != nil && ip.To4() != nil && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && (cfg.IncludeLoopback || !ip.IsLoopback()) {
				return net.JoinHostPort(ip.String(), "0"), nil
			}
		}
	}
	return "", errors.New("no allowed IPv4 interface for prediction")
}

func (p *icePeer) punchTLS(a *iceAttempt) *tls.Config {
	c := p.coordinator.identity.Clone()
	c.NextProtos, c.InsecureSkipVerify, c.ClientAuth = []string{holeALPN}, true, tls.RequireAnyClientCert
	c.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("peer certificate missing")
		}
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), a.ready.PeerFingerprint) {
			return &Fault{Code: "peer_identity_mismatch", Message: "对端身份与协调服务记录不一致"}
		}
		return nil
	}
	return c
}
