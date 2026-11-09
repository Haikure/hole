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
	return p.coordinator.send(m)
}

// One attempt reader owns negotiation. Keep readiness from a faster peer for
// the next round, so a delayed failure cannot discard its new calibration.
func (p *icePeer) waitNATPunch(ctx context.Context, a *iceAttempt, kind string, round int) (*natPunchSignal, error) {
	return p.waitNATPunchTrial(ctx, a, kind, round, 0)
}

func (p *icePeer) waitNATPunchTrial(ctx context.Context, a *iceAttempt, kind string, round, trial int) (*natPunchSignal, error) {
	for i, m := range a.punchBuffered {
		if m.Kind == "failed" && m.Round == round {
			a.punchBuffered = append(a.punchBuffered[:i], a.punchBuffered[i+1:]...)
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
			if m.Punch.Kind == "abort" {
				return nil, errors.New("peer stopped prediction")
			}
			if m.Punch.Kind == "failed" && m.Punch.Round == round {
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

func (p *icePeer) prepareNATPunch(a *iceAttempt, cfg Config) (*natPunchSession, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 3*natprobe.DefaultTimeout+2*time.Second)
	defer cancel()
	local := p.coordinator.localNAT()
	if len(natURLs(cfg.ICE)) > 0 {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for local.ProbedAt == "" {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
				local = p.coordinator.localNAT()
			}
		}
	}
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
		return nil, fmt.Errorf("%w: public IP changed", errNATPunchModel)
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
			}
		}
	}()
	refresh := false
	lastServer := ""
	for round := 1; round <= 3; round++ {
		if scheduled {
			if e := p.refreshNATPunchModel(a, platform, cfg, s, round, refresh); e != nil {
				return nil, e
			}
		}
		model := s.local
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
			}()
			ctx, stopAttempt := context.WithTimeout(ctx, s.config.Timeout.Duration())
			defer stopAttempt()
			servers, err := punchSTUNServers(ctx, platform, natURLs(cfg.ICE), lastServer)
			if err != nil {
				return nil, err
			}
			bindAddress, err := natPunchBind(ctx, platform, cfg.ICE)
			if err != nil {
				return nil, err
			}
			open := func() (net.PacketConn, error) {
				pc, e := platform.ListenPacket(ctx, "udp4", bindAddress)
				if e == nil {
					held = append(held, pc)
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
			for range count {
				pc, e := open()
				if e != nil {
					return nil, e
				}
				sockets = append(sockets, pc)
			}
			plans, e := p.calibrateNATPunch(ctx, a, s, round, servers, sockets, open, &lastServer, &diagnostic)
			if e != nil {
				return nil, e
			}
			diagnostic.LocalAddress = sockets[0].LocalAddr().String()
			diagnostic.Sockets = len(sockets)
			diagnostic.Stage = "coordination"
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
			want, e := makeNATPunchPlan(netip.AddrPortFrom(remoteIP, uint16(peerPlan.Port)), s.remote, s.mode, peerPlan.Sockets, s.config.ScanWidth)
			if e != nil {
				return nil, e
			}
			if *want != *peerPlan {
				return nil, errors.New("peer punch plan differs from model")
			}
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
			if s.mode == "pool" && want.Target < want.Port {
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
			diagnostic.Stage, diagnostic.Dialer = "udp", dialer
			diagnostic.TargetFirst, diagnostic.TargetLast = targets[0].String(), targets[len(targets)-1].String()
			attempt.diagnostic = diagnostic
			roundCtx, stopRound := context.WithTimeout(ctx, s.config.RoundTimeout.Duration())
			connection, e := attempt.run(roundCtx, dialer, round, s.key, targets, p.punchTLS(a))
			stopRound()
			diagnostic = attempt.diagnostic
			if diagnostic.QUICDialStarted {
				diagnostic.Stage = "quic"
			}
			if e == nil {
				diagnostic.Stage = "mux"
				deadline, _ := ctx.Deadline()
				mux, muxErr := newMuxPeerPrepared(a.ctx, p, connection, a.ready, nil, deadline)
				if muxErr != nil {
					attempt.close()
					return nil, muxErr
				}
				selected = sockets[attempt.selected]
				p.mu.Lock()
				p.stats.PathType, p.stats.LocalType, p.stats.RemoteType = "direct", "srflx", "srflx"
				p.stats.AddressFamily = "IPv4"
				p.stats.LocalAddress, p.stats.RemoteAddress = selected.LocalAddr().String(), connection.RemoteAddr().String()
				p.mu.Unlock()
				live := &liveICETransport{ready: a.ready, mux: mux, quic: attempt.transports[attempt.selected], session: connection, cancel: a.cancel}
				live.fixedCleanup = func() { attempt.close(); _ = selected.Close() }
				diagnostic.Stage = "connected"
				return live, nil
			}
			attempt.close()
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
		if e := p.sendNATPunch(a, natPunchSignal{Kind: "failed", Round: round}); e != nil {
			return nil, e
		}
	}
	return nil, errors.New("prediction rounds exhausted")
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
	c.NextProtos, c.InsecureSkipVerify, c.ClientAuth = []string{iceALPN}, true, tls.RequireAnyClientCert
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
