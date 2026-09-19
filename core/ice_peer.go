package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/quic-go/quic-go"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type iceAttempt struct {
	ctx    context.Context
	cancel context.CancelFunc
	ready  iceSignalMessage
	events chan iceSignalMessage
	failed chan error
}
type liveICETransport struct {
	ready      iceSignalMessage
	mux        *muxPeer
	agent      *ice.Agent
	connection *ice.Conn
	packet     *icePacketConn
	quic       *quic.Transport
	session    *quic.Conn
	cancel     context.CancelFunc
	expires    int64
	once       sync.Once
}

func (t *liveICETransport) close() {
	t.once.Do(func() { t.cancel(); t.mux.close(); _ = t.quic.Close(); _ = t.packet.Close(); _ = t.agent.Close() })
}

type icePeer struct {
	coordinator         *iceCoordinator
	ctx                 context.Context
	cancel              context.CancelFunc
	mu                  sync.Mutex
	ready               iceSignalMessage
	pending             *iceAttempt
	active              *liveICETransport
	stats               PeerTransportSnapshot
	requestedGeneration uint64
	closed              bool
	terminal            bool
	online              bool
	workers             sync.WaitGroup
}

func newICEPeer(c *iceCoordinator, m iceSignalMessage) *icePeer {
	ctx, cancel := context.WithCancel(c.ctx)
	p := &icePeer{coordinator: c, ctx: ctx, cancel: cancel, stats: PeerTransportSnapshot{PeerID: m.PeerDevice, TransportID: m.TransportID, Profile: ProfileICE, State: "connecting"}}
	p.workers.Add(1)
	go func() { defer p.workers.Done(); p.maintain() }()
	return p
}
func (p *icePeer) peerID() string { p.mu.Lock(); defer p.mu.Unlock(); return p.ready.PeerDevice }
func (p *icePeer) records() []peerMappingRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]peerMappingRecord{}, p.ready.Mappings...)
}
func (p *icePeer) mapping(id string) (peerMappingRecord, bool) {
	for _, r := range p.records() {
		if r.ID == id && p.coordinator.allowed(r) {
			return r, true
		}
	}
	return peerMappingRecord{}, false
}
func (p *icePeer) allowNew() bool {
	p.mu.Lock()
	until := p.ready.LeaseUntil
	closed := p.closed || !p.online
	p.mu.Unlock()
	return !closed && p.coordinator.signalOnline() && until > time.Now().UnixMilli()
}
func (p *icePeer) authorized(r peerMappingRecord) bool {
	p.mu.Lock()
	until := p.ready.LeaseUntil
	closed := p.closed
	p.mu.Unlock()
	current, ok := p.mapping(r.ID)
	return !closed && ok && current == r && until > time.Now().UnixMilli()
}
func (p *icePeer) update(m iceSignalMessage) {
	p.mu.Lock()
	if p.closed || m.TransportGeneration < p.ready.TransportGeneration {
		p.mu.Unlock()
		return
	}
	old := p.ready
	wasOnline := p.online
	p.online = true
	identityChanged := old.PeerFingerprint != "" && (old.PeerFingerprint != m.PeerFingerprint || old.PeerRuntimeID != m.PeerRuntimeID)
	p.ready = m
	p.stats.LeaseUntil = m.LeaseUntil
	p.stats.RelayPolicy = m.RelayPolicy
	p.stats.MappingCount = len(m.Mappings)
	active := p.active
	if identityChanged {
		p.active = nil
		if p.pending != nil {
			p.pending.cancel()
			p.pending = nil
		}
	}
	same := old.TransportGeneration == m.TransportGeneration
	need := !same && !p.closed
	if need {
		if p.pending != nil {
			p.pending.cancel()
		}
		ctx, cancel := context.WithCancel(p.ctx)
		a := &iceAttempt{ctx: ctx, cancel: cancel, ready: m, events: make(chan iceSignalMessage, maxICECandidates+4), failed: make(chan error, 1)}
		p.pending = a
		p.requestedGeneration = 0
		p.terminal = false
		p.stats.State = "connecting"
		p.stats.Phase = m.Phase
		p.stats.Generation = m.TransportGeneration
		p.stats.Error = nil
		p.stats.LocalCandidates = 0
		p.stats.RemoteCandidates = 0
		p.stats.LocalRelayProtocol = ""
		p.stats.RelayProtocol = ""
		p.stats.RemoteRelayProtocol = ""
		p.stats.DatagramLimit = 0
		p.stats.CandidatePairs = nil
		p.workers.Add(1)
		go func() { defer p.workers.Done(); p.attempt(a) }()
	}
	p.mu.Unlock()
	if need || !wasOnline || !slices.Equal(old.Mappings, m.Mappings) {
		p.stateChanged()
	}
	if identityChanged {
		if active != nil {
			active.close()
		}
		for _, r := range old.Mappings {
			p.coordinator.sessions.terminate(r.ID, old.PeerFingerprint)
		}
	}
	for _, r := range old.Mappings {
		keep := false
		for _, n := range m.Mappings {
			if n == r {
				keep = true
				break
			}
		}
		if !keep {
			p.coordinator.sessions.terminate(r.ID, old.PeerFingerprint)
		}
	}
	for _, r := range m.Mappings {
		p.coordinator.sessions.claimPeer(r.ID, m.PeerDevice, m.PeerFingerprint)
	}
	if active != nil && !identityChanged {
		active.mux.syncMappings()
	}
	if same {
		p.mu.Lock()
		retry := p.active == nil && p.pending == nil && !p.terminal
		if retry {
			p.requestedGeneration = 0
		}
		p.mu.Unlock()
		if retry {
			p.restart("direct", "retry")
		}
	}
}
func (p *icePeer) receive(m iceSignalMessage) {
	p.mu.Lock()
	a := p.pending
	p.mu.Unlock()
	if a == nil || m.TransportGeneration != a.ready.TransportGeneration {
		return
	}
	select {
	case <-a.ctx.Done():
	case a.events <- m:
	default:
		select {
		case a.failed <- errors.New("ICE candidate queue limit"):
		default:
		}
	}
}
func (p *icePeer) offline(reason string) {
	p.mu.Lock()
	p.online = false
	p.requestedGeneration = 0
	if p.pending != nil {
		p.pending.cancel()
		p.pending = nil
	}
	p.stats.State = "paused"
	active := p.active
	records := append([]peerMappingRecord{}, p.ready.Mappings...)
	fp := p.ready.PeerFingerprint
	p.mu.Unlock()
	p.stateChanged()
	if reason == "mapping_removed" || reason == "peer_runtime_changed" || reason == "revoked" {
		for _, r := range records {
			p.coordinator.sessions.terminate(r.ID, fp)
		}
		if active != nil {
			active.close()
		}
	}
}
func (p *icePeer) refreshPolicy() {
	p.mu.Lock()
	active := p.active
	p.mu.Unlock()
	if active != nil {
		active.mux.syncMappings()
	}
}
func (p *icePeer) emitMapping(id, state, protocol string) {
	p.mu.Lock()
	peer := p.ready.PeerDevice
	path := p.stats.RemoteAddress
	p.mu.Unlock()
	p.coordinator.emit(Event{Kind: "mapping", MappingID: id, Protocol: protocol, State: state, Peer: peer, Path: path, Profile: ProfileICE})
}
func (p *icePeer) mappingError(id string, err error) {
	p.coordinator.emit(Event{Kind: "mapping", MappingID: id, State: "error", Profile: ProfileICE, Error: classifyError(err)})
}
func (p *icePeer) restart(phase, reason string) {
	if !p.coordinator.signalOnline() {
		return
	}
	p.mu.Lock()
	if p.closed || !p.online || p.ready.TransportGeneration == 0 || p.requestedGeneration == p.ready.TransportGeneration {
		p.mu.Unlock()
		return
	}
	m := p.ready
	p.requestedGeneration = m.TransportGeneration
	p.stats.RetryCount++
	p.mu.Unlock()
	request := signalMessage("transport_restart")
	request.TransportID = m.TransportID
	request.ExpectedGeneration = m.TransportGeneration
	request.Phase = phase
	request.Reason = reason
	if err := p.coordinator.send(request); err != nil {
		p.mu.Lock()
		if p.requestedGeneration == m.TransportGeneration {
			p.requestedGeneration = 0
		}
		p.mu.Unlock()
	}
}
func (p *icePeer) transportEvent(a *iceAttempt, state string, fault *Fault) {
	p.coordinator.emit(Event{Kind: "transport", State: state, Peer: a.ready.PeerDevice, Profile: ProfileICE,
		Phase: a.ready.Phase, TransportGeneration: a.ready.TransportGeneration, Error: fault})
}

// Notify hosts when snapshot-visible transport state changes. CLI can remain
// entirely event-driven; byte counters, RTT samples and lease renewals do not
// emit these notifications.
func (p *icePeer) stateChanged() {
	if p.coordinator.emit == nil {
		return
	}
	p.coordinator.emit(Event{Kind: "transport", State: "changed", Peer: p.peerID(), Profile: ProfileICE})
}
func (p *icePeer) failed(a *iceAttempt, err error) {
	if a.ctx.Err() != nil {
		return
	}
	fault := classifyError(err)
	if fault.Code == "network_error" {
		fault = &Fault{Code: "ice_path_failed", Message: "当前网络路径尚未连通，正在尝试下一条路径"}
	}
	p.mu.Lock()
	if p.pending != a {
		p.mu.Unlock()
		return
	}
	p.pending = nil
	p.stats.State = "reconnecting"
	p.stats.Error = fault
	p.terminal = fault.Code == "protocol_mismatch" || fault.Code == "peer_identity_mismatch"
	fatal := p.terminal
	if fatal {
		p.stats.State = "error"
	}
	p.mu.Unlock()
	state := "retrying"
	if fatal {
		state = "error"
	}
	p.transportEvent(a, state, fault)
	if fatal {
		report := signalMessage("transport_failed")
		report.TransportID = a.ready.TransportID
		report.TransportGeneration = a.ready.TransportGeneration
		report.Code = fault.Code
		_ = p.coordinator.send(report)
	} else {
		next := nextICEPhase(a.ready.Phase, a.ready.RelayPolicy)
		if fault.Code == "relay_credentials_unavailable" {
			next = "direct"
		}
		p.restart(next, "path_failed")
	}
}
func (p *icePeer) attempt(a *iceAttempt) {
	owned := false
	defer func() {
		if !owned {
			a.cancel()
		}
	}()
	if a.ready.RetryAfterMS > 0 {
		timer := time.NewTimer(time.Duration(a.ready.RetryAfterMS) * time.Millisecond)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	cfg := p.coordinator.request().Config
	platform, err := p.coordinator.pin(a.ctx)
	if err != nil {
		p.failed(a, err)
		return
	}
	network, err := newICENetwork(a.ctx, platform)
	if err != nil {
		p.failed(a, err)
		return
	}
	urls := []*stun.URI{}
	for _, raw := range cfg.ICE.STUNURLs {
		u, e := stun.ParseURI(raw)
		if e == nil {
			urls = append(urls, u)
		}
	}
	kinds := []ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive}
	servers, expires, relayState := p.coordinator.servers()
	if a.ready.Phase != "direct" {
		if cfg.TURN.Mode == "worker" && len(servers) == 0 {
			p.mu.Lock()
			if p.pending == a {
				p.stats.State = "waiting_credentials"
			}
			p.mu.Unlock()
			p.transportEvent(a, "waiting_credentials", nil)
		}
		servers, expires, relayState, err = p.coordinator.relayServers(a.ctx)
		if err != nil {
			p.failed(a, err)
			return
		}
	}
	relayURLs := selectRelayURLs(servers, a.ready.Phase)
	relayCount := len(relayURLs)
	urls = append(urls, relayURLs...)
	if relayCount > 0 {
		kinds = append(kinds, ice.CandidateTypeRelay)
	}
	if a.ready.Phase != "direct" && relayCount == 0 {
		p.failed(a, &Fault{Code: "relay_unavailable", Message: "此阶段没有可用的中继地址，继续下一条路径"})
		return
	}
	if cfg.ICE.RelayOnly {
		if relayCount == 0 {
			p.failed(a, &Fault{Code: "relay_unavailable", Message: "此阶段没有可用的中继服务器"})
			return
		}
		kinds = []ice.CandidateType{ice.CandidateTypeRelay}
	}
	// Credentials have their own wait budget. Announce checking and measure
	// connection time only after that wait, before gathering opens sockets.
	p.mu.Lock()
	if p.pending == a {
		p.stats.State = "connecting"
	}
	p.mu.Unlock()
	started := time.Now()
	p.transportEvent(a, "checking", nil)
	logger := logging.NewDefaultLoggerFactory()
	logger.DefaultLogLevel = logging.LogLevelDisabled
	logger.Writer = io.Discard
	allow := stringSet(cfg.ICE.InterfaceAllowlist)
	controlling := a.ready.InitiatorID == p.coordinator.deviceID()
	relayPhase := a.ready.Phase != "direct"
	options := []ice.AgentOption{ice.WithNet(network), ice.WithUrls(urls), ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}), ice.WithCandidateTypes(kinds), ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled), ice.WithLoggerFactory(logger), ice.WithSTUNGatherTimeout(cfg.ICE.GatherTimeout.Duration()), ice.WithKeepaliveInterval(10 * time.Second), ice.WithDisconnectedTimeout(20 * time.Second), ice.WithFailedTimeout(20 * time.Second), ice.WithInterfaceFilter(func(name string) bool {
		if len(allow) > 0 {
			return allow[name]
		}
		return !isDefaultExcludedInterface(name)
	}), ice.WithIPFilter(func(ip net.IP) bool {
		return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && (cfg.ICE.IncludeLoopback || !ip.IsLoopback())
	})}
	if relayPhase {
		// Give single-leg (asymmetric) pairs time to succeed before any
		// relay-involving pair may be nominated; see relayNominationWait.
		options = append(options, ice.WithRelayAcceptanceMinWait(relayNominationWait))
	}
	if cfg.ICE.IncludeLoopback {
		options = append(options, ice.WithIncludeLoopback())
	}
	if a.ready.Phase == "relay_tls" || a.ready.Phase == "relay_tls_443" || a.ready.Phase == "relay_tcp" || a.ready.Phase == "relay_tcp_80" {
		options = append(options, ice.WithTURNTransportProtocols([]ice.NetworkType{ice.NetworkTypeTCP4, ice.NetworkTypeTCP6}))
	} else {
		options = append(options, ice.WithTURNTransportProtocols([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}))
	}
	agent, err := ice.NewAgentWithOptions(options...)
	if err != nil {
		p.failed(a, err)
		return
	}
	defer func() {
		if !owned {
			_ = agent.Close()
		}
	}()
	fail := func(err error) {
		select {
		case a.failed <- err:
		default:
		}
	}
	_ = agent.OnConnectionStateChange(func(state ice.ConnectionState) {
		if state == ice.ConnectionStateFailed {
			fail(errors.New("ICE connectivity failed"))
		}
	})
	var localCount int
	var localMu sync.Mutex
	localGathered := make(chan struct{})
	var gatheredOnce sync.Once
	_ = agent.OnCandidate(func(candidate ice.Candidate) {
		m := signalMessage("ice_candidate")
		m.TransportID = a.ready.TransportID
		m.TransportGeneration = a.ready.TransportGeneration
		m.ToPeerID = a.ready.PeerDevice
		if candidate == nil {
			m.EndOfCandidates = true
			gatheredOnce.Do(func() { close(localGathered) })
		} else {
			localMu.Lock()
			localCount++
			n := localCount
			localMu.Unlock()
			if n > maxICECandidates {
				return
			}
			m.ICECandidate = candidate.Marshal()
			p.mu.Lock()
			if p.pending == a {
				p.stats.LocalCandidates = n
			}
			p.mu.Unlock()
		}
		if e := p.coordinator.send(m); e != nil {
			fail(e)
		}
	})
	_ = agent.OnSelectedCandidatePairChange(func(local, remote ice.Candidate) {
		p.mu.Lock()
		if p.ready.TransportGeneration != a.ready.TransportGeneration {
			p.mu.Unlock()
			return
		}
		selectedICEPair(&p.stats, local, remote)
		active := p.active != nil && p.active.ready.TransportGeneration == a.ready.TransportGeneration
		p.mu.Unlock()
		if active {
			p.stateChanged()
		}
	})
	ufrag, pwd, err := agent.GetLocalUserCredentials()
	if err != nil {
		p.failed(a, err)
		return
	}
	description := signalMessage("ice_description")
	description.TransportID = a.ready.TransportID
	description.TransportGeneration = a.ready.TransportGeneration
	description.ToPeerID = a.ready.PeerDevice
	description.Ufrag = ufrag
	description.Pwd = pwd
	if err = p.coordinator.send(description); err != nil {
		p.failed(a, err)
		return
	}
	if err = agent.GatherCandidates(); err != nil {
		p.failed(a, err)
		return
	}
	remoteCredentials := make(chan iceSignalMessage, 1)
	eventCtx, stopEvents := context.WithCancel(a.ctx)
	defer stopEvents()
	eventsDone := make(chan struct{})
	remoteGathered := make(chan struct{})
	go func() {
		defer close(eventsDone)
		described := false
		var oldUfrag, oldPwd string
		early := []ice.Candidate{}
		remoteCount := 0
		seen := map[string]bool{}
		for {
			select {
			case <-eventCtx.Done():
				return
			case m := <-a.events:
				if m.Type == "ice_description" {
					if len(m.Ufrag) < 4 || len(m.Ufrag) > 256 || len(m.Pwd) < 22 || len(m.Pwd) > 256 {
						fail(errors.New("invalid ICE description"))
						return
					}
					if described {
						if oldUfrag != m.Ufrag || oldPwd != m.Pwd {
							fail(errors.New("ICE credentials changed within generation"))
						}
						continue
					}
					described = true
					oldUfrag, oldPwd = m.Ufrag, m.Pwd
					remoteCredentials <- m
					for _, candidate := range early {
						if e := agent.AddRemoteCandidate(candidate); e != nil {
							fail(e)
						}
					}
					early = nil
				} else if m.Type == "ice_candidate" && m.EndOfCandidates {
					select {
					case <-remoteGathered:
					default:
						close(remoteGathered)
					}
				} else if m.Type == "ice_candidate" {
					if seen[m.ICECandidate] {
						continue
					}
					seen[m.ICECandidate] = true
					remoteCount++
					if remoteCount > maxICECandidates || len(m.ICECandidate) > 2048 {
						fail(errors.New("ICE candidate limit"))
						return
					}
					candidate, e := ice.UnmarshalCandidate(m.ICECandidate)
					if e != nil || candidate.Component() != 1 {
						fail(errors.New("invalid ICE candidate"))
						return
					}
					if described {
						if e = agent.AddRemoteCandidate(candidate); e != nil {
							fail(e)
						}
					} else {
						early = append(early, candidate)
					}
					p.mu.Lock()
					if p.pending == a {
						p.stats.RemoteCandidates = remoteCount
					}
					p.mu.Unlock()
				}
			}
		}
	}()
	defer func() { stopEvents(); <-eventsDone }()
	timeout := cfg.ICE.ConnectivityTimeout.Duration() + cfg.ICE.GatherTimeout.Duration()
	if a.ready.Phase == "direct" {
		timeout = cfg.ICE.DirectProbeTimeout.Duration()
	}
	checkCtx, stopCheck := context.WithTimeout(a.ctx, timeout)
	defer stopCheck()
	var remote iceSignalMessage
	select {
	case <-checkCtx.Done():
		p.failed(a, checkCtx.Err())
		return
	case err = <-a.failed:
		p.failed(a, err)
		return
	case remote = <-remoteCredentials:
	}
	var conn *ice.Conn
	if controlling {
		conn, err = agent.StartDial(remote.Ufrag, remote.Pwd)
	} else {
		conn, err = agent.StartAccept(remote.Ufrag, remote.Pwd)
	}
	if err != nil {
		p.failed(a, err)
		return
	}
	// Once both sides have announced end-of-candidates, every pair the two
	// sets can form is already on the checklist. If all of them fail, the
	// remaining phase budget buys nothing; move to the next path. The verdict
	// travels on its own channel: a late one must not outlive a connection
	// that a revived pair completed in the meantime.
	exhausted := make(chan error, 1)
	exhaustionCtx, stopExhaustion := context.WithCancel(checkCtx)
	defer stopExhaustion()
	go func() {
		select {
		case <-exhaustionCtx.Done():
			return
		case <-localGathered:
		}
		select {
		case <-exhaustionCtx.Done():
			return
		case <-remoteGathered:
		}
		watchICEExhaustion(exhaustionCtx, agent, func(err error) {
			select {
			case exhausted <- err:
			default:
			}
		})
	}()
	connected := make(chan error, 1)
	go func() { connected <- agent.AwaitConnect(checkCtx) }()
	select {
	case err = <-connected:
	case err = <-a.failed:
	case err = <-exhausted:
	}
	stopExhaustion()
	if err != nil {
		p.failed(a, err)
		return
	}
	packet := newICEPacketConn(conn)
	var packetConn net.PacketConn = packet
	if !relayPhase {
		base := ""
		if pair, e := agent.GetSelectedCandidatePair(); e == nil && pair != nil {
			base = candidateSocket(pair.Local)
		}
		if sockets := network.socketsFor(base); len(sockets) > 0 {
			packetConn = &mtuProbingConn{icePacketConn: packet, sockets: sockets}
		}
	}
	quicTransport := &quic.Transport{Conn: packetConn}
	defer func() {
		if !owned {
			_ = quicTransport.Close()
			_ = packet.Close()
		}
	}()
	tlsConfig := p.coordinator.identity.Clone()
	tlsConfig.NextProtos = []string{iceALPN}
	tlsConfig.InsecureSkipVerify = true
	tlsConfig.ClientAuth = tls.RequireAnyClientCert
	var identityMismatch atomic.Bool
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			identityMismatch.Store(true)
			return &Fault{Code: "peer_identity_mismatch", Message: "对端未提供证书"}
		}
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		if hex.EncodeToString(sum[:]) != a.ready.PeerFingerprint {
			identityMismatch.Store(true)
			return &Fault{Code: "peer_identity_mismatch", Message: "对端身份与协调服务记录不一致"}
		}
		return nil
	}
	qc := iceQUICConfig(a.ready.Phase)
	handshakeCtx, stopHandshake := context.WithTimeout(a.ctx, 10*time.Second)
	defer stopHandshake()
	var connection *quic.Conn
	if controlling {
		connection, err = quicTransport.Dial(handshakeCtx, packet.remote, tlsConfig, qc)
	} else {
		var listener *quic.Listener
		listener, err = quicTransport.Listen(tlsConfig, qc)
		if err == nil {
			connection, err = listener.Accept(handshakeCtx)
		}
	}
	if err != nil {
		if identityMismatch.Load() {
			err = &Fault{Code: "peer_identity_mismatch", Message: "对端身份校验未通过"}
		}
		p.failed(a, err)
		return
	}
	if !connection.ConnectionState().SupportsDatagrams {
		_ = connection.CloseWithError(1, "DATAGRAM required")
		p.failed(a, &Fault{Code: "protocol_mismatch", Message: "对端未启用 QUIC 数据报"})
		return
	}
	selectedPair, selectedPairErr := agent.GetSelectedCandidatePair()
	if selectedPairErr == nil && selectedPair != nil {
		p.mu.Lock()
		if p.pending == a {
			selectedICEPair(&p.stats, selectedPair.Local, selectedPair.Remote)
		}
		p.mu.Unlock()
	}
	mux, err := newMuxPeer(a.ctx, p, connection, a.ready)
	if err != nil {
		_ = connection.CloseWithError(1, "mux negotiation failed")
		p.failed(a, err)
		return
	}
	// Only the selected local relay allocation depends on these credentials.
	// A direct path (or a relay used only by the remote peer) must not restart
	// simply because an unrelated TURN credential was refreshed.
	usedExpires := int64(0)
	if pair, e := agent.GetSelectedCandidatePair(); e == nil && pair != nil && pair.Local.Type() == ice.CandidateTypeRelay {
		usedExpires = expires
	}
	live := &liveICETransport{ready: a.ready, mux: mux, agent: agent, connection: conn, packet: packet, quic: quicTransport, session: connection, cancel: a.cancel, expires: usedExpires}
	p.mu.Lock()
	if p.pending != a || p.closed {
		p.mu.Unlock()
		live.close()
		return
	}
	old := p.active
	p.active = live
	p.pending = nil
	p.stats.State = "active"
	p.stats.Error = nil
	p.stats.ConnectMS = time.Since(started).Milliseconds()
	p.stats.RelayState = relayState
	p.stats.TURNExpiresAt = expires
	p.mu.Unlock()
	owned = true
	mux.syncMappings()
	if old != nil {
		old.close()
	}
	p.stateChanged()
	status := signalMessage("transport_established")
	status.TransportID = a.ready.TransportID
	status.TransportGeneration = a.ready.TransportGeneration
	_ = p.coordinator.send(status)
	select {
	case <-mux.ctx.Done():
	case <-a.ctx.Done():
	case err = <-a.failed:
	}
	live.close()
	p.mu.Lock()
	current := p.active == live
	if current {
		p.active = nil
		p.stats.State = "reconnecting"
	}
	p.mu.Unlock()
	if current && p.ctx.Err() == nil {
		p.stateChanged()
		p.restart("direct", "path_lost")
	}
}
func (p *icePeer) maintain() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			active := p.active
			lease := p.ready.LeaseUntil
			pending := p.pending
			terminal := p.terminal
			p.mu.Unlock()
			if active != nil {
				if lease <= time.Now().UnixMilli() {
					active.close()
					continue
				}
				active.mux.syncMappings()
				_, expires, _ := p.coordinator.servers()
				if active.expires > 0 && expires > active.expires && pending == nil {
					p.restart(active.ready.Phase, "turn_refresh")
				}
			} else if pending == nil && !terminal {
				p.restart("direct", "retry")
			}
		}
	}
}
func (p *icePeer) snapshot() PeerTransportSnapshot {
	p.mu.Lock()
	s := p.stats
	active := p.active
	pending := p.pending
	online := p.online
	if s.Error != nil {
		f := *s.Error
		s.Error = &f
	}
	p.mu.Unlock()
	if active != nil && active.mux.ctx.Err() == nil {
		s.State = "active"
		if !online {
			s.State = "paused"
		}
		if pending != nil {
			s.State = "switching"
		}
		s.Generation = active.ready.TransportGeneration
		s.Phase = active.ready.Phase
		s.RelayPolicy = active.ready.RelayPolicy
		if pending != nil {
			s.PendingPhase = pending.ready.Phase
		}
		if pair, err := active.agent.GetSelectedCandidatePair(); err == nil && pair != nil {
			// selectedICEPair refreshes the pair-derived fields. The remote TURN
			// access protocol comes from the mux hello and is not present in ICE
			// SDP, so preserve that separately reported value.
			remoteRelayProtocol := s.RemoteRelayProtocol
			selectedICEPair(&s, pair.Local, pair.Remote)
			s.RemoteRelayProtocol = remoteRelayProtocol
		}
		if pair, ok := active.agent.GetSelectedCandidatePairStats(); ok {
			s.RTTMS = int64(pair.CurrentRoundTripTime * 1000)
		}
		s.CandidatePairs = candidatePairSnapshots(active.agent)
		if active.session != nil {
			s.DatagramLimit = datagramLimit(active.session)
		}
		s.BytesSent = active.connection.BytesSent()
		s.BytesReceived = active.connection.BytesReceived()
		s.DroppedDatagrams = active.mux.dropped.Load()
		active.mux.mu.Lock()
		s.ActiveChannels = 0
		for _, channel := range active.mux.channels {
			if channel.ready.Load() && channel.ctx.Err() == nil {
				s.ActiveChannels++
			}
		}
		active.mux.mu.Unlock()
	}
	_, expires, state := p.coordinator.servers()
	s.RelayState = state
	s.TURNExpiresAt = expires
	return s
}
func (p *icePeer) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.online = false
	p.stats.State = "closed"
	pending, active := p.pending, p.active
	p.mu.Unlock()
	p.cancel()
	if pending != nil {
		pending.cancel()
	}
	if active != nil {
		active.close()
	}
	p.workers.Wait()
	p.stateChanged()
}

func (p *icePeer) renewLease(lease transportLease) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed && p.online && lease.Generation == p.ready.TransportGeneration && lease.Until > p.ready.LeaseUntil {
		p.ready.LeaseUntil = lease.Until
		p.stats.LeaseUntil = lease.Until
	}
}
