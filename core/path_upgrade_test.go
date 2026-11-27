package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/transport/v5"
)

func TestPathUpgradeSchedulingAndFailureIsolation(t *testing.T) {
	for _, path := range []string{"relay", "hole", "peer_relay", "direct"} {
		t.Run(path, func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			p.ready.Profile, p.ready.PathUpgrade, p.ready.InitiatorID = ProfileICE, true, c.deviceID()
			p.active = &liveICETransport{ready: p.ready, pathType: path, mux: &muxPeer{ctx: c.ctx}}
			now := time.Now()
			p.nextUpgrade = now.Add(time.Second)
			p.probeUpgrade(now)
			if len(c.sink.messages) != 0 {
				t.Fatal("probe started before retry deadline")
			}
			p.probeUpgrade(now.Add(time.Second))
			p.probeUpgrade(now.Add(time.Second))
			if path == "direct" {
				if len(c.sink.messages) != 0 {
					t.Fatal("healthy direct path was reprobed")
				}
				return
			}
			if len(c.sink.messages) != 1 {
				t.Fatal("probe was missing or duplicated")
			}
			if m := <-c.sink.messages; m.Reason != "auto_upgrade" {
				t.Fatal(m)
			}
			ctx, cancel := context.WithCancel(c.ctx)
			a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready}
			a.ready.UpgradeProbe, a.ready.Phase = true, "peer_relay"
			p.pending = a
			active := p.active
			p.failed(a, &Fault{Code: "peer_identity_mismatch", Message: "candidate identity mismatch"})
			if p.active != active || active.mux.ctx.Err() != nil || p.pending != nil || p.terminal {
				t.Fatal("candidate failure disturbed the active path")
			}
			if m := <-c.sink.messages; m.Reason != "upgrade_failed" {
				t.Fatal(m)
			}
		})
	}
}

func TestPathUpgradeWaitsForCurrentCommit(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready, upgradeCommit: make(chan struct{}), failed: make(chan error, 1)}
	p.pending = a
	p.stats.PathType = "direct"
	live := &liveICETransport{mux: &muxPeer{ctx: ctx}}
	done := make(chan error, 1)
	go func() { done <- p.prepareUpgrade(a, live) }()
	m := <-c.sink.messages
	if m.Type != "transport_upgrade_prepared" {
		t.Fatal(m)
	}
	p.commitUpgrade(a.ready.TransportGeneration + 1)
	select {
	case err := <-done:
		t.Fatal("stale commit activated candidate", err)
	default:
	}
	p.commitUpgrade(a.ready.TransportGeneration)
	p.commitUpgrade(a.ready.TransportGeneration)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for range 8 {
		p.deferUpgradeLocked(time.Now())
	}
	if delay := time.Until(p.nextUpgrade); delay > 5*time.Minute || delay < 4*time.Minute {
		t.Fatal(delay)
	}
}

func TestPathUpgradeTURNRefreshRetainsActiveRound(t *testing.T) {
	for _, pairing := range []string{relayPairingAll, ""} {
		t.Run("pairing="+pairing, func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			ctx, cancel := context.WithCancel(c.ctx)
			p.ctx = ctx
			p.ready.Profile, p.ready.PathUpgrade = ProfileICE, true
			p.ready.Mappings = nil
			p.ready.RelayPairing = pairing
			p.ready.RelayOrder = []string{"udp", "tcp"}
			activeReady := p.ready
			round := 2
			activeReady.Phase, activeReady.RelayRound = "relay_tcp", &round
			active := &liveICETransport{ready: activeReady, pathType: "relay", mux: &muxPeer{ctx: ctx, owner: p}, expires: time.Now().Add(time.Minute).UnixMilli()}
			p.active = active
			candidateRound := 0
			p.ready.TransportGeneration++
			p.ready.RelayRound = &candidateRound
			p.ready.UpgradeProbe, p.ready.UpgradeIdle = true, true
			p.nextUpgrade = time.Now().Add(time.Hour)
			c.turnExpires = time.Now().Add(time.Hour).UnixMilli()
			done := make(chan struct{})
			go func() { defer close(done); p.maintain() }()
			defer func() { cancel(); <-done }()
			select {
			case m := <-c.sink.messages:
				if m.Type != "transport_restart" || m.Reason != "turn_refresh" || m.TransportID != p.ready.TransportID || m.ExpectedGeneration != p.ready.TransportGeneration || m.RelayRound == nil || *m.RelayRound != round {
					t.Fatalf("renewal did not retain active round and current generation: %+v", m)
				}
				phase := activeReady.Phase
				if pairing == relayPairingAll {
					phase = ""
				}
				if m.Phase != phase || active.mux.ctx.Err() != nil {
					t.Fatalf("renewal changed phase or closed the active path: %+v", m)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("TURN renewal was not requested")
			}
		})
	}
}

func TestPathUpgradeCommitReplayReannouncesInstalledTransport(t *testing.T) {
	for _, path := range []string{"direct", "peer_relay"} {
		t.Run(path, func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			ctx, cancel := context.WithCancel(c.ctx)
			defer cancel()
			p.ready.Profile, p.ready.PathUpgrade = ProfileICE, true
			p.ready.Mappings = nil
			p.retiring = &liveICETransport{ready: p.ready}
			p.ready.TransportGeneration++
			p.ready.UpgradeProbe, p.ready.UpgradeCommit = true, true
			p.active = &liveICETransport{ready: p.ready, pathType: path, mux: &muxPeer{ctx: ctx, owner: p}}
			p.stats.PathType = "hole" // A replay must report the installed path.
			active, retiring := p.active, p.retiring
			for range 2 {
				p.update(p.ready)
				select {
				case m := <-c.sink.messages:
					if m.Type != "transport_established" || m.TransportID != p.ready.TransportID || m.TransportGeneration != p.ready.TransportGeneration || m.PathType != path {
						t.Fatalf("incorrect installation acknowledgement: %+v", m)
					}
				default:
					t.Fatal("commit replay did not acknowledge the installed path")
				}
				if p.active != active || p.retiring != retiring || p.pending != nil {
					t.Fatal("commit replay replaced or retired a path")
				}
			}
			p.commitUpgrade(p.ready.TransportGeneration - 1)
			p.commitUpgrade(p.ready.TransportGeneration + 1)
			cancel()
			p.commitUpgrade(p.ready.TransportGeneration)
			if len(c.sink.messages) != 0 {
				t.Fatal("acknowledged a stale generation or a closed path")
			}
		})
	}
}

// Describe loopback explicitly: these tests exercise real sockets and QUIC,
// without requiring host netlink access or Internet connectivity.
type upgradeTestPlatform struct {
	relayLegPlatform
	address string
	nat     *punchSimPlatform
}

func (p *upgradeTestPlatform) InterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	return []InterfaceSnapshot{{Name: "lo", Up: true, Loopback: true, Addresses: []string{p.address}}}, nil
}
func (p *upgradeTestPlatform) NATIPv4Available() bool { return p.nat != nil }
func (*upgradeTestPlatform) NetworkBinding() bool     { return true }
func (p *upgradeTestPlatform) SnapshotNetwork(context.Context) (Platform, NetworkSnapshot, error) {
	return p, NetworkSnapshot{Available: true, Handle: "upgrade-test"}, nil
}

type upgradeTURNNetwork struct{ transport.Net }

func (upgradeTURNNetwork) ListenPacket(network, address string) (net.PacketConn, error) {
	return net.ListenPacket(network, address)
}

type upgradePacket struct {
	*net.UDPConn
	platform *upgradeTestPlatform
}

func (p *upgradeTestPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	if p.nat != nil && address == "0.0.0.0:0" {
		return p.nat.ListenPacket(ctx, network, address)
	}
	pc, err := p.DefaultPlatform.ListenPacket(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &upgradePacket{UDPConn: pc.(*net.UDPConn), platform: p}, nil
}

func (p *upgradePacket) allowed(addr net.Addr) bool {
	host, _, _ := net.SplitHostPort(addr.String())
	return p.platform.allowAll.Load() || p.platform.allowed[addr.String()] || host == "127.0.0.2"
}
func (p *upgradePacket) WriteTo(b []byte, addr net.Addr) (int, error) {
	if !p.allowed(addr) {
		return len(b), nil
	}
	return p.UDPConn.WriteTo(b, addr)
}
func (p *upgradePacket) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	if !p.allowed(addr) {
		return len(b), nil
	}
	return p.UDPConn.WriteToUDP(b, addr)
}
func (p *upgradePacket) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (int, int, error) {
	if !p.allowed(addr) {
		return len(b), len(oob), nil
	}
	return p.UDPConn.WriteMsgUDP(b, oob, addr)
}

func TestPathUpgradeRelayToNodeToDirectPreservesTCP(t *testing.T) {
	for _, path := range []string{"relay", "hole", "ice_node", "nat_punch", "delayed_commit", "lost_established"} {
		t.Run(path, func(t *testing.T) { testPathUpgradeRelayToNodeToDirectPreservesTCP(t, path) })
	}
}

func testPathUpgradeRelayToNodeToDirectPreservesTCP(t *testing.T, initialPath string) {
	natPunch := initialPath == "nat_punch"
	lostEstablished := initialPath == "lost_established"
	if lostEstablished {
		t.Setenv("HOLE_FIXTURE_DROP_UPGRADE_ESTABLISHED", "1")
	}
	if initialPath == "delayed_commit" || natPunch {
		t.Setenv("HOLE_FIXTURE_COMMIT_DELAY", "800")
	}
	if initialPath == "delayed_commit" || lostEstablished {
		initialPath = "relay"
	}
	iceNode := initialPath == "ice_node" || natPunch
	if iceNode || natPunch {
		initialPath = "relay"
	}
	server, hibernate := actualWorkerFixture(t)
	turnURL, stopTURN := startTURNRelay(t, "udp", upgradeTURNNetwork{})
	defer stopTURN()
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	_, turnAddress, _ := strings.Cut(turnURL, ":")
	turnAddress, _, _ = strings.Cut(turnAddress, "?")
	relayAddress := fmt.Sprintf("127.0.0.1:%d", unusedUDPPort(t))
	holeAddress := fmt.Sprintf("127.0.0.1:%d", unusedUDPPort(t))
	allowAll := &atomic.Bool{}
	platform := func() *upgradeTestPlatform {
		return &upgradeTestPlatform{relayLegPlatform: relayLegPlatform{allowed: map[string]bool{turnAddress: true, relayAddress: true, holeAddress: true}, allowAll: allowAll}, address: "127.0.0.1"}
	}
	relayPlatform := platform()
	relayPlatform.allowAll = &atomic.Bool{}
	relayPlatform.allowAll.Store(true)
	leftPlatform, rightPlatform := platform(), platform()
	var punchNetwork *natNetwork
	if natPunch {
		punchNetwork, _, _ = simulatedNATs([2]int{1, 1}, [2]int{}, 0)
		punchNetwork.blockOneWay = true
		leftPlatform.nat = &punchSimPlatform{nat: punchNetwork.sides[0]}
		rightPlatform.nat = &punchSimPlatform{nat: punchNetwork.sides[1]}
	}
	a, b, relay := NewEngine(Options{Platform: leftPlatform}), NewEngine(Options{Platform: rightPlatform}), NewEngine(Options{Platform: relayPlatform})
	defer a.Close()
	defer b.Close()
	defer relay.Close()
	defer func() {
		if !t.Failed() {
			return
		}
		for name, e := range map[string]*Engine{"a": a, "b": b, "relay": relay} {
			for _, p := range e.Snapshot().PeerTransports {
				for _, event := range e.NATPunchProcess(p.TransportID, 0).Events {
					switch event.Kind {
					case "stage", "finished", "signal_error", "round_failed", "quic_established", "ice_state":
						t.Logf("%s generation=%d stage=%s round=%d: %s %s", name, event.Generation, event.Stage, event.Round, event.Title, event.Detail)
					}
				}
			}
			for {
				select {
				case event := <-e.Events():
					t.Logf("%s %s %s: %v %s", name, event.Kind, event.State, event.Error, event.Message)
				default:
					goto drained
				}
			}
		drained:
		}
	}()
	ra, rb, rr := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta"), iceFixtureRequest(server, "relay")
	for _, r := range []*Request{&ra, &rb, &rr} {
		r.Config.Voice.Enabled = true
		r.Config.ICE.PortMapping = "off"
		r.Config.ICE.DirectProbeTimeout = ConfigDuration(600 * time.Millisecond)
		r.Config.ICE.ConnectivityTimeout = ConfigDuration(8 * time.Second)
		r.Config.ICE.GatherTimeout = ConfigDuration(time.Second)
		r.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{turnURL}, Username: "user", Credential: "secret", Order: []string{"udp"}}
	}
	if natPunch {
		for _, r := range []*Request{&ra, &rb} {
			r.Config.ICE.InterfaceAllowlist = nil
			r.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478", "stun:192.0.2.2:3478", "stun:192.0.2.3:3478"}
			r.Config.ICE.Punch.Timeout = ConfigDuration(2 * time.Second)
			r.Config.ICE.Punch.RoundTimeout = ConfigDuration(500 * time.Millisecond)
			r.Config.ICE.Punch.DualSockets = 2
			r.Config.ICE.Punch.DualRetrySockets = 4
		}
	}
	rr.Config.Transport.FixedEndpoint = &FixedEndpointConfig{Listen: relayAddress, Advertise: relayAddress}
	if iceNode {
		rr.Config.Transport.FixedEndpoint = nil
		relayPlatform.address = "127.0.0.2"
	}
	if initialPath == "hole" {
		hole := NewEngine(Options{AllowRelayServe: true, Platform: relayPlatform})
		defer hole.Close()
		rh := iceFixtureRequest(server, "hole-node")
		rh.Config.Transport.FixedEndpoint = &FixedEndpointConfig{Listen: holeAddress, Advertise: holeAddress}
		rh.Config.Relay.Serve.Enabled = true
		if err := hole.Start(rh); err != nil {
			t.Fatal(err)
		}
		waitSnapshotWithin(t, hole, 5*time.Second, func(s Snapshot) bool { return s.SignalState == "joined" })
		for _, r := range []*Request{&ra, &rb} {
			r.Config.Relay.Device = "hole-node"
			r.Config.Relay.Order = []string{"hole"}
			r.Config.TURN.Mode = "off"
		}
	}
	port := unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "echo", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "echo", Expose: HostPort{"127.0.0.1", port}}}
	if err = a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err = b.Start(rb); err != nil {
		t.Fatal(err)
	}
	waitPath := func(e *Engine, path string) {
		t.Helper()
		waitSnapshotWithin(t, e, 15*time.Second, func(s Snapshot) bool {
			for _, p := range s.PeerTransports {
				if (p.PeerID == "alpha" || p.PeerID == "beta") && p.PathType == path && p.State == "active" && p.ActiveChannels == 1 {
					return true
				}
			}
			return false
		})
	}
	waitPath(a, initialPath)
	waitPath(b, initialPath)
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	transferWithin := func(value string, timeout time.Duration) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := io.WriteString(conn, value); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(value))
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != value {
			t.Fatalf("TCP: %q %v", buf, err)
		}
	}
	transfer := func(value string) { t.Helper(); transferWithin(value, 8*time.Second) }
	transfer(initialPath)
	if err = relay.Start(rr); err != nil {
		t.Fatal(err)
	}
	waitPath(a, "peer_relay")
	waitPath(b, "peer_relay")
	transfer("node relay")
	a.mu.Lock()
	coordinator := a.run.ice
	a.mu.Unlock()
	forceProbe := func() {
		t.Helper()
		for _, p := range coordinator.peerList() {
			p.mu.Lock()
			target := p.ready.PeerDevice == "beta"
			if target {
				p.nextUpgrade = time.Now().Add(-time.Second)
			}
			p.mu.Unlock()
			if target {
				p.probeUpgrade(time.Now())
				return
			}
		}
		t.Fatal("missing target peer")
	}
	var target *icePeer
	for _, p := range coordinator.peerList() {
		if p.peerID() == "beta" {
			target = p
		}
	}
	target.mu.Lock()
	retained := target.active
	target.mu.Unlock()
	forceProbe()
	deadline := time.Now().Add(10 * time.Second)
	for {
		target.mu.Lock()
		idle, active := target.ready.UpgradeIdle, target.active
		target.mu.Unlock()
		if active != retained || retained.mux.ctx.Err() != nil {
			t.Fatal("background ICE interrupted the established node relay")
		}
		// Check the data path during discovery, not only after TCP replay has
		// had time to hide an interruption at the end of the failed probe.
		transferWithin("background ICE and punch", 500*time.Millisecond)
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background probe did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	transfer("failed direct probe retained node")
	if natPunch {
		s := target.snapshot()
		target.mu.Lock()
		generation := target.ready.TransportGeneration
		target.mu.Unlock()
		if len(s.PunchDiagnostics) < 3 {
			t.Fatalf("background probe did not exercise all NAT prediction rounds: %+v", s.PunchDiagnostics)
		}
		last := s.PunchDiagnostics[len(s.PunchDiagnostics)-1]
		if last.Generation != generation || last.Round != 3 || last.ProbesSent == 0 {
			t.Fatalf("background prediction did not complete all three rounds: %+v", last)
		}
	}
	hibernate()
	if err = coordinator.send(signalMessage("transport_sync")); err != nil {
		t.Fatal(err)
	}
	transfer("hibernated node relay")
	if natPunch {
		punchNetwork.mu.Lock()
		punchNetwork.blockOneWay = false
		punchNetwork.mu.Unlock()
	} else {
		allowAll.Store(true)
	}
	target.mu.Lock()
	wantGeneration := target.ready.TransportGeneration + 1
	target.mu.Unlock()
	if natPunch {
		wantGeneration++ // Ordinary ICE fails before the prediction succeeds.
	}
	forceProbe()
	deadline = time.Now().Add(20 * time.Second)
	for {
		transferWithin("probe and commit", 500*time.Millisecond)
		left, right := false, false
		for _, s := range a.Snapshot().PeerTransports {
			left = left || s.PeerID == "beta" && s.PathType == "direct" && s.State == "active"
		}
		for _, s := range b.Snapshot().PeerTransports {
			right = right || s.PeerID == "alpha" && s.PathType == "direct" && s.State == "active"
		}
		if left && right {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("direct upgrade did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitPath(a, "direct")
	waitPath(b, "direct")
	if generation := target.snapshot().Generation; generation != wantGeneration {
		t.Fatalf("path upgrade triggered an unexpected recovery: generation %d, want %d", generation, wantGeneration)
	}
	transfer("direct")
	// The lost-report fixture disconnects beta's WebSocket after installation.
	// Its reconnect must acknowledge the replayed commit and retire the old path.
	waitSnapshotWithin(t, a, 5*time.Second, func(Snapshot) bool {
		target.mu.Lock()
		released := target.retiring == nil
		target.mu.Unlock()
		return released && retained.mux.ctx.Err() != nil
	})
	if lostEstablished {
		transfer("replayed installation acknowledgement")
		if generation := target.snapshot().Generation; generation != wantGeneration {
			t.Fatalf("commit replay changed generation: got %d, want %d", generation, wantGeneration)
		}
	}
	if accepted.Load() != 1 {
		t.Fatalf("upstream TCP reopened %d times", accepted.Load())
	}
}
