package core

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type roomTestPeer struct {
	link    roomLink
	mu      sync.Mutex
	frames  []roomFrame
	deliver func(roomFrame)
}

func (p *roomTestPeer) roomLink() roomLink { return p.link }
func (p *roomTestPeer) sendRoomFrame(f roomFrame) bool {
	p.mu.Lock()
	p.frames = append(p.frames, f)
	p.mu.Unlock()
	if p.deliver != nil {
		p.deliver(f)
	}
	return true
}

type roomTestSignalPlatform struct {
	DefaultPlatform
	mu     sync.Mutex
	signal net.Conn
}

func (p *roomTestSignalPlatform) DialSignal(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := p.DefaultPlatform.DialSignal(ctx, network, address)
	if err == nil {
		p.mu.Lock()
		p.signal = conn
		p.mu.Unlock()
	}
	return conn, err
}

func (p *roomTestSignalPlatform) disconnectSignal(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	conn := p.signal
	p.signal = nil
	p.mu.Unlock()
	if conn == nil {
		t.Fatal("no signaling connection to interrupt")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func roomTestCoordinator(t *testing.T, device string) *iceCoordinator {
	t.Helper()
	identity, err := makeTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	r := iceFixtureRequest("ws://fixture.invalid/ws", device)
	r.Config.Room = "room-control-test"
	c := newICECoordinator(context.Background(), r, DefaultPlatform{}, nil, identity, strings.Repeat(device, 32), 1, func(Event) {}, func(NetworkSnapshot) {})
	t.Cleanup(c.cancel)
	c.room.workerVersion = "1.1.0"
	c.room.workerKey = &identity.Certificates[0].PrivateKey.(*ecdsa.PrivateKey).PublicKey
	c.online = true
	c.sink = &iceSignalSink{ctx: c.ctx, messages: make(chan iceSignalMessage, 256)}
	return c
}

func TestRoomControlTransportReadyBindsOnlyAcceptedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invalid bool
		closed  bool
		accept  bool
	}{
		{name: "older_generation"},
		{name: "invalid_declaration", invalid: true},
		{name: "closed_peer", closed: true},
		{name: "new_generation", accept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, current, other := roomTestCoordinator(t, "a"), roomTestCoordinator(t, "b"), roomTestCoordinator(t, "c")
			round := 0
			ready := iceSignalMessage{
				SignalMessage:   SignalMessage{Type: "transport_ready", PeerDevice: "b", PeerFingerprint: current.fingerprint},
				PeerCoreVersion: CoreVersion, TransportID: "ab", TransportGeneration: 2, PeerRuntimeID: current.runtimeID,
				SessionVersion: sessionProtocol, Profile: ProfileICE, InitiatorID: "a", Phase: "direct",
				RelayPolicy: RelayPolicyUDPTCPTLS, RelayPairing: relayPairingAll, RelayRound: &round,
				RelayOrder: []string{}, PeerRelayOrder: []string{}, RoomControl: true,
			}
			p := &icePeer{coordinator: c, ctx: c.ctx, ready: ready, online: true, closed: tc.closed}
			c.peers[ready.TransportID] = p
			c.room.directory["b"] = voiceRoomMember{DeviceName: "b", RuntimeID: current.runtimeID, Certificate: current.room.certificate,
				CertFingerprint: current.fingerprint, CoreVersion: CoreVersion, TransportEpoch: 1}
			c.room.nodes["b"] = roomNodeRecord{node: roomNode{DeviceName: "b", RuntimeID: current.runtimeID, CoreVersion: CoreVersion,
				WorkerVersion: "1.1.0", Epoch: 1, Revision: 2, SignalOnline: true, Resident: true}}
			c.room.peers["ab"] = &roomTestPeer{link: roomLink{PeerID: "b", RuntimeID: current.runtimeID, TransportID: "ab", Generation: 2}}
			if !c.room.hasSignalVia() {
				t.Fatal("fixture has no signaling route")
			}
			next := ready
			next.TransportGeneration = 1
			next.PeerRuntimeID, next.PeerFingerprint = other.runtimeID, other.fingerprint
			if tc.invalid || tc.closed || tc.accept {
				next.TransportGeneration = 3
			}
			if tc.invalid {
				next.SessionVersion = 0
			}
			if err := c.handle(next, c.request(), c.platform); (err != nil) != tc.invalid {
				t.Fatalf("unexpected validation result: %v", err)
			}
			member := c.room.directory["b"]
			if tc.accept {
				if p.ready.TransportGeneration != 3 || member.RuntimeID != other.runtimeID || member.CertFingerprint != other.fingerprint {
					t.Fatal("accepted generation did not update both identities")
				}
				if _, ok := c.room.nodes["b"]; ok {
					t.Fatal("replacement identity retained the previous room node")
				}
				return
			}
			if p.ready.TransportGeneration != 2 || p.ready.PeerRuntimeID != current.runtimeID {
				t.Fatal("rejected update changed the transport identity")
			}
			if member.RuntimeID != current.runtimeID || member.CertFingerprint != current.fingerprint || !c.room.hasSignalVia() {
				t.Fatalf("rejected update changed the directory or signaling route: %+v, %+v", member, c.room.snapshot().SignalVia)
			}
		})
	}
}

func TestRoomControlDelegatedPeerLossWaitsForConfirmedRecovery(t *testing.T) {
	a, carrier, lost := roomTestCoordinator(t, "a"), roomTestCoordinator(t, "b"), roomTestCoordinator(t, "c")
	for _, member := range []*iceCoordinator{a, carrier, lost} {
		a.room.directory[member.deviceID()] = voiceRoomMember{DeviceName: member.deviceID(), RuntimeID: member.runtimeID,
			CoreVersion: CoreVersion, Certificate: member.room.certificate, CertFingerprint: member.fingerprint, TransportEpoch: 1, GroupID: "a"}
	}
	a.online, a.room.online, a.sink = false, false, nil
	a.room.nodes["b"] = roomNodeRecord{node: roomNode{DeviceName: "b", RuntimeID: carrier.runtimeID,
		CoreVersion: CoreVersion, WorkerVersion: "1.1.0", Epoch: 1, SignalOnline: true}}
	bridge := &roomTestPeer{link: roomLink{PeerID: "b", RuntimeID: carrier.runtimeID, TransportID: "ab", Generation: 1}}
	edge := &roomTestPeer{link: roomLink{PeerID: "c", RuntimeID: lost.runtimeID, TransportID: "ac", Generation: 1}}
	a.room.peers["ab"], a.room.peers["ac"] = bridge, edge
	p := &icePeer{coordinator: a, ctx: a.ctx, online: true, ready: iceSignalMessage{
		SignalMessage: SignalMessage{PeerDevice: "c"}, TransportID: "ac", TransportGeneration: 1,
		PeerRuntimeID: lost.runtimeID, Profile: ProfileICE, Phase: "direct", Mappings: []peerMappingRecord{{ID: "test"}},
	}}
	a.peers["ac"] = p
	assertDelegated := func() {
		t.Helper()
		for _, reason := range []string{"path_lost", "retry", "peer_relay_failed"} {
			p.restartFrom(p.ready, "direct", reason)
		}
		if a.room.forceSignal || a.signalNeeded() || !a.room.waitForSignal() || a.room.keepBackup {
			t.Fatal("an unreachable peer or background report woke delegated signaling")
		}
	}
	a.room.detach(edge)
	assertDelegated() // The directory still contains the peer's old group.
	lateRestart := signalMessage("transport_restart")
	lateRestart.TransportID, lateRestart.ExpectedGeneration, lateRestart.Reason = "ac", 1, "path_lost"
	_ = a.send(lateRestart) // The request may have been prepared before WSS closed.
	assertDelegated()
	if len(a.pendingSignals) != 0 {
		t.Fatal("path loss queued an unnecessary restart")
	}
	local, online, offline := a.room.directory["a"], a.room.directory["b"], a.room.directory["c"]
	message := iceSignalMessage{StateRevision: 1, RoomState: []voiceRoomMember{local, online}}
	if !a.room.installDirectory(message, nil) {
		t.Fatal("offline directory was rejected")
	}
	assertDelegated()
	for _, kind := range []string{"nat_status", "transport_established", "peer_relay_failed"} {
		report := signalMessage(kind)
		report.TransportID, report.TransportGeneration = p.ready.TransportID, p.ready.TransportGeneration
		if err := a.send(report); err != nil {
			t.Fatal(err)
		}
	}
	assertDelegated()
	if len(a.pendingSignals) != 3 {
		t.Fatal("deferred reports were lost")
	}
	// An upload racing with WSS closure is already covered by node flooding.
	_ = a.send(signalMessage("room_graph"))
	assertDelegated()
	// A still-online peer in a separate component needs a new negotiation.
	offline.GroupID = "c"
	message.StateRevision++
	message.RoomState = append(message.RoomState, offline)
	if !a.room.installDirectory(message, nil) || !a.signalNeeded() {
		t.Fatal("confirmed component split did not require path recovery")
	}
	p.restartFrom(p.ready, "direct", "retry")
	if !a.room.forceSignal || a.pendingSignals["transport_restart/ac"].ExpectedGeneration != 1 {
		t.Fatal("confirmed peer recovery did not wake direct signaling")
	}
	// Queued reports must survive until the next direct join.
	a.online = true
	a.sink = &iceSignalSink{ctx: a.ctx, messages: make(chan iceSignalMessage, 16)}
	joined := signalMessage("joined")
	joined.SignalVersion = 2
	if err := a.handle(joined, a.request(), a.platform); err != nil {
		t.Fatal(err)
	}
	if len(a.pendingSignals) != 0 || len(a.sink.messages) != 5 {
		t.Fatal("rejoining did not flush reports, restart and transport sync")
	}
}

func TestRoomControlStaleOfflineRestartDoesNotWakeSignaling(t *testing.T) {
	for _, state := range []string{"closed", "offline", "old_generation", "replaced_attempt", "healthy_path"} {
		t.Run(state, func(t *testing.T) {
			c := roomTestCoordinator(t, "a")
			c.online, c.sink = false, nil
			p := &icePeer{coordinator: c, ctx: c.ctx, online: true,
				ready: iceSignalMessage{TransportID: "ab", TransportGeneration: 2}}
			message := p.ready
			var failed *iceAttempt
			switch state {
			case "closed":
				p.closed = true
			case "offline":
				p.online = false
			case "old_generation":
				message.TransportGeneration--
			case "replaced_attempt":
				failed = &iceAttempt{}
				p.pending = &iceAttempt{}
			case "healthy_path":
				p.active = &liveICETransport{mux: &muxPeer{ctx: c.ctx}}
			}
			p.restartWithRoundFrom(message, nil, "direct", "path_lost", failed)
			if c.room.forceSignal || len(c.pendingSignals) != 0 {
				t.Fatal("a stale callback woke direct signaling")
			}
		})
	}
}

type roomNoNetworkPlatform struct{ DefaultPlatform }

func (p roomNoNetworkPlatform) SnapshotNetwork(context.Context) (Platform, NetworkSnapshot, error) {
	return p, NetworkSnapshot{Handle: "room-control-test", Available: false}, nil
}
func (roomNoNetworkPlatform) NetworkBinding() bool   { return true }
func (roomNoNetworkPlatform) NATIPv4Available() bool { return false }

func TestRoomControlDelegatedLocalConfigCompletesOnce(t *testing.T) {
	for _, undo := range []bool{false, true} {
		t.Run(fmt.Sprintf("coalesced_undo_%t", undo), func(t *testing.T) {
			c, peer := roomTestCoordinator(t, "a"), roomTestCoordinator(t, "b")
			c.initial.Config.SessionTimeout = 10 * time.Minute
			c.desired = c.initial
			c.configAccepted(c.initial)
			c.platform = roomNoNetworkPlatform{}
			c.sessions = newSessionManager(c.initial.Config.SessionTimeout, sessionOptions{platform: c.platform, shared: true})
			defer c.sessions.close()
			c.online, c.room.online, c.sink = false, false, nil
			c.room.directory["b"] = voiceRoomMember{DeviceName: "b", RuntimeID: peer.runtimeID, CoreVersion: CoreVersion,
				CertFingerprint: peer.fingerprint, TransportEpoch: 1}
			c.room.nodes["b"] = roomNodeRecord{node: roomNode{DeviceName: "b", RuntimeID: peer.runtimeID, CoreVersion: CoreVersion,
				WorkerVersion: "1.1.0", Epoch: 1, Revision: 1, SignalOnline: true, Resident: true}}
			c.room.peers["ab"] = &roomTestPeer{link: roomLink{PeerID: "b", RuntimeID: peer.runtimeID, TransportID: "ab", Generation: 1}}
			events := make(chan Event, 128)
			c.emit = func(event Event) { events <- event }
			next := c.initial
			next.Config.SessionTimeout += time.Minute
			if undo {
				c.submit(next)
				next = c.initial
				c.submit(next)
			}
			done := make(chan struct{})
			go func() { defer close(done); _ = c.run() }()
			defer func() { c.cancel(); <-done }()
			completions := 0
			checkEvent := func(event Event) {
				t.Helper()
				if event.Kind == "config" && event.State == "applied" {
					completions++
					if event.appliedConfig == nil || !sameEffective(*event.appliedConfig, next) {
						t.Fatalf("completed a superseded config: %+v", event)
					}
				}
			}
			waitDelegated := func(wantCompletions int) {
				t.Helper()
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				for {
					select {
					case event := <-events:
						checkEvent(event)
						if event.Kind == "signal" && event.State == "delegated" && completions >= wantCompletions {
							return
						}
					case <-timer.C:
						t.Fatalf("missing delegated completion: got %d, want %d", completions, wantCompletions)
					}
				}
			}
			if !undo {
				waitDelegated(0)
				c.submit(next)
			}
			waitDelegated(1)
			if c.signalOnline() {
				t.Fatal("local update resumed direct signaling")
			}
			c.sessions.mu.Lock()
			actualTimeout := c.sessions.timeout
			c.sessions.mu.Unlock()
			if actualTimeout != next.Config.SessionTimeout {
				t.Fatalf("session timeout not applied: %s", actualTimeout)
			}
			c.room.notify()
			waitDelegated(1)
			c.cancel()
			<-done
			for len(events) > 0 {
				checkEvent(<-events)
			}
			if completions != 1 {
				t.Fatalf("graph wake or coalesced edits duplicated completion: %d", completions)
			}
		})
	}
}

func TestRoomControlWakeAcknowledgesExpectedPeerAndHoldsSignaling(t *testing.T) {
	a, b, other := roomTestCoordinator(t, "a"), roomTestCoordinator(t, "b"), roomTestCoordinator(t, "c")
	for _, owner := range []*iceCoordinator{a, b, other} {
		for _, member := range []*iceCoordinator{a, b, other} {
			owner.room.directory[member.deviceID()] = voiceRoomMember{DeviceName: member.deviceID(), RuntimeID: member.runtimeID, CoreVersion: CoreVersion, Certificate: member.room.certificate, CertFingerprint: member.fingerprint, TransportEpoch: 1}
		}
	}
	toB := &roomTestPeer{link: roomLink{PeerID: "b", RuntimeID: b.runtimeID, TransportID: "ab", Generation: 1}}
	toA := &roomTestPeer{link: roomLink{PeerID: "a", RuntimeID: a.runtimeID, TransportID: "ab", Generation: 1}}
	a.room.peers["ab"], b.room.peers["ab"] = toB, toA
	toA.deliver = func(frame roomFrame) { a.room.accept(frame, nil) }
	toB.deliver = func(frame roomFrame) {
		if frame.Kind == "wake" {
			forged, err := other.room.makeFrame("wake_ack", struct {
				RequestID string `json:"request_id"`
			}{frame.ID}, a.deviceID(), a.runtimeID)
			if err != nil {
				t.Fatal(err)
			}
			a.room.accept(forged, nil)
			select {
			case <-a.room.waking[frame.ID].done:
				t.Fatal("another member acknowledged the requested peer's wake")
			default:
			}
		}
		b.room.accept(frame, nil)
	}
	a.room.wakeExistingPeers()
	if len(a.room.waking) != 0 || !b.room.forceSignal {
		t.Fatal("wake acknowledgment did not complete or trigger direct signaling")
	}
	b.room.synchronized()
	if !b.signalNeeded() {
		t.Fatal("an old-generation sync released signaling before reconfiguration")
	}
	toA.link.Generation++
	if b.signalNeeded() {
		t.Fatal("the replacement connection left a permanent signaling reservation")
	}
	if b.room.keepBackup {
		t.Fatal("reconfiguration created a permanent backup")
	}
}

func TestRoomControlActualWorkerWakeAndSessionContinuity(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	signalPlatform := &roomTestSignalPlatform{}
	a, b, c := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{Platform: signalPlatform, RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	defer c.Close()
	ra, rb, rc := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta"), iceFixtureRequest(server, "gamma")
	target := ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: echo.Addr().(*net.TCPAddr).Port}
	portB, portC := unusedTCPPort(t), unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "first", Service: target}}
	rb.Config.Consume = []Consume{{ID: "first", Expose: HostPort{"127.0.0.1", portB}}}
	rb.Config.Provide = []Provide{{ID: "wake-beta", Service: target}}
	rc.Config.Consume = []Consume{{ID: "wake-beta", Expose: HostPort{"127.0.0.1", portC}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	ready := func(s Snapshot) bool {
		return s.Room != nil && len(s.Room.SignalVia) > 0 && len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 1
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "joined" && ready(s)
	})
	// Healthy members retain their Worker sockets. Delegation follows an
	// unexpected signal loss while another member remains reachable.
	signalPlatform.disconnectSignal(t)
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "delegated" && ready(s)
	})
	first, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", portB))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	transfer := func(conn net.Conn, text string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(conn, text); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, len(text))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Fatal(err)
		}
		if string(body) != text {
			t.Fatal("session bytes changed")
		}
	}
	transfer(first, "before new member")
	beforeGeneration := b.Snapshot().PeerTransports[0].Generation
	if err := b.RenominateTransports(); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "joined" && ready(s) && s.PeerTransports[0].Generation > beforeGeneration
	})
	transfer(first, "manual reselection woke delegated signaling")
	signalPlatform.disconnectSignal(t)
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "delegated" && ready(s)
	})
	b.mu.Lock()
	coordinator := b.run.ice
	b.mu.Unlock()
	for _, peer := range coordinator.peerList() {
		peer.mu.Lock()
		peer.ready.LeaseUntil = time.Now().Add(-time.Hour).UnixMilli()
		peer.mu.Unlock()
	}
	hibernate()
	if err := c.Start(rc); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s Snapshot) bool {
		return s.SignalState == "joined" && len(s.PeerTransports) == 1 && s.PeerTransports[0].ActiveChannels == 1
	})
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "joined" && len(s.PeerTransports) == 2
	})
	second, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", portC))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	transfer(second, "member woke beta through alpha")
	transfer(first, "original TCP connection survived")
	if len(b.Snapshot().PeerTransports) != 2 {
		t.Fatal("new relationship replaced the existing peer")
	}
	ra.Config.Provide = []Provide{}
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		for _, peer := range s.PeerTransports {
			if peer.PeerID == "alpha" {
				return peer.State == "active" && peer.MappingCount == 0 && peer.ActiveChannels == 0
			}
		}
		return false
	})
	transfer(second, "control link survived removal of the other service")
	if room := b.Snapshot().Room; room == nil || room.GroupID != "alpha" {
		t.Fatal("service removal partitioned the room control graph", room)
	}
}

func TestRoomControlMultihopRecoveryAndSplit(t *testing.T) {
	a, b, c := roomTestCoordinator(t, "a"), roomTestCoordinator(t, "b"), roomTestCoordinator(t, "c")
	for _, peer := range []*iceCoordinator{a, b, c} {
		c.room.directory[peer.deviceID()] = voiceRoomMember{DeviceName: peer.deviceID(), RuntimeID: peer.runtimeID, CoreVersion: CoreVersion, CertFingerprint: peer.fingerprint, Certificate: peer.room.certificate, TransportEpoch: 1}
	}
	linkAB := roomLink{PeerID: "b", RuntimeID: b.runtimeID, TransportID: "ab", Generation: 1}
	linkBA := roomLink{PeerID: "a", RuntimeID: a.runtimeID, TransportID: "ab", Generation: 1}
	linkBC := roomLink{PeerID: "c", RuntimeID: c.runtimeID, TransportID: "bc", Generation: 1}
	bridge := &roomTestPeer{link: roomLink{PeerID: "b", RuntimeID: b.runtimeID, TransportID: "bc", Generation: 1}}
	c.room.attach(bridge)
	sendNode := func(owner *iceCoordinator, revision uint64, online, recovering bool, links ...roomLink) roomFrame {
		t.Helper()
		frame, err := owner.room.makeFrame("node", roomNode{DeviceName: owner.deviceID(), RuntimeID: owner.runtimeID, CoreVersion: CoreVersion, WorkerVersion: "1.1.0", Epoch: 1, Revision: revision, SignalOnline: online, SignalRecovering: recovering, Resident: online, Neighbours: links}, "", "")
		if err != nil {
			t.Fatal(err)
		}
		c.room.accept(frame, bridge)
		return frame
	}
	sendNode(b, 1, false, false, linkBA, linkBC)
	sendNode(a, 1, true, false, linkAB)
	c.online = false
	c.room.online = false
	if !c.room.hasSignalVia() || !c.room.waitForSignal() {
		t.Fatal("three-node chain did not reach its signaling carrier")
	}
	sendNode(a, 2, false, true, linkAB)
	if c.room.hasSignalVia() || !c.room.waitForSignal() {
		t.Fatal("first reconnect attempt should remain owned by the reachable last carrier")
	}
	frame, err := a.room.makeFrame("recover", struct {
		Sequence uint64 `json:"sequence,string"`
	}{3}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	c.room.accept(frame, bridge)
	select {
	case <-c.room.wake:
	default:
		t.Fatal("first failed carrier attempt did not wake the other member")
	}
	if c.room.waitForSignal() || !c.room.keepBackup {
		t.Fatal("parallel recovery was not enabled")
	}
	bad, err := b.room.makeFrame("node", roomNode{DeviceName: "b", RuntimeID: b.runtimeID, CoreVersion: CoreVersion, WorkerVersion: "1.1.0", Epoch: 1, Revision: 2, Neighbours: []roomLink{}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(bad.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	body["revision"] = "999"
	data, _ = json.Marshal(body)
	bad.Body = base64.StdEncoding.EncodeToString(data)
	c.room.accept(bad, bridge)
	if c.room.nodes["b"].node.Revision != 1 {
		t.Fatal("tampered origin declaration was accepted")
	}
	c.room.detach(bridge)
	if c.room.hasSignalVia() || len(c.room.snapshot().Members) != 1 {
		t.Fatal("unreachable cached carrier survived a graph split")
	}
}

func TestLocalAuthorizationAndRelayIdle(t *testing.T) {
	message := iceSignalMessage{CoreVersion: "1.1.0", PeerCoreVersion: "1.1.0", WorkerVersion: "1.1.0", LeaseUntil: 0}
	ctx, cancel := authorizationContext(context.Background(), message)
	defer cancel()
	if ctx.Err() != nil {
		t.Fatal("zero central lease canceled a modern traversal")
	}
	if _, deadline := ctx.Deadline(); deadline {
		t.Fatal("modern traversal retained a central lease deadline")
	}
	grant := testHoleGrant()
	grant.LocalManagement = true
	f := &holeForwarder{routes: map[[16]byte]*holeRoute{}, grants: map[string]holeGrantMark{}, free: make(chan *holeFrame, holePairQueue), limit: 2}
	message.Hole = grant
	message.TransportID = "pair"
	message.TransportGeneration = grant.Generation
	if err := f.install(message); err != nil {
		t.Fatal(err)
	}
	route := f.order[0]
	if route.expired(time.Now()) {
		t.Fatal("new local route expired immediately")
	}
	route.activity[0] = time.Now().Add(-localRelayIdleTimeout - time.Second)
	if !route.expired(time.Now()) {
		t.Fatal("local route did not reclaim a missing endpoint")
	}
	f.removeLocked(route)
	if err := f.install(message); err == nil {
		t.Fatal("expired local route was revived by replaying its old grant")
	}
	grant.Generation++
	grant.Route = "11234567890123456789012345678901"
	message.TransportGeneration = grant.Generation
	if err := f.install(message); err != nil {
		t.Fatal("fresh route generation could not replace a revoked route", err)
	}
}
