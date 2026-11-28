package core

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/transport/v5"
)

func TestParallelTURNRefreshRetainsActive(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	turnURL, stop := startTURNRelay(t, "udp", upgradeTURNNetwork{})
	defer stop()
	_, address, _ := strings.Cut(turnURL, ":")
	address, _, _ = strings.Cut(address, "?")
	var peers []*icePeer
	var engines []*Engine
	for _, name := range []string{"alpha", "beta"} {
		platform := &upgradeTestPlatform{relayLegPlatform: relayLegPlatform{allowed: map[string]bool{address: true}, allowAll: &atomic.Bool{}}, address: "127.0.0.1"}
		e := NewEngine(Options{Platform: platform})
		defer e.Close()
		engines = append(engines, e)
		r := iceFixtureRequest(server, name)
		r.Config.Relay.Device = "hole-node"
		r.Config.Relay.Order = []string{"hole", "udp"}
		r.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{turnURL}, Username: "user", Credential: "secret", Order: []string{"udp"}}
		r.Config.ICE.PortMapping = "off"
		r.Config.ICE.DirectProbeTimeout = ConfigDuration(time.Second)
		r.Config.Voice.Enabled = true
		if err := e.Start(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range engines {
		waitSnapshotWithin(t, e, 10*time.Second, func(Snapshot) bool {
			e.mu.Lock()
			c := e.run.ice
			e.mu.Unlock()
			if c == nil {
				return false
			}
			ps := c.peerList()
			if len(ps) != 1 {
				return false
			}
			p := ps[0]
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.active != nil && p.active.pathType == "relay" && p.ready.UpgradeIdle
		})
		e.mu.Lock()
		c := e.run.ice
		e.mu.Unlock()
		peers = append(peers, c.peerList()[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var releases []func()
	var old []*liveICETransport
	for _, p := range peers {
		release, err := p.coordinator.natAllocation.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		releases = append(releases, release)
		p.mu.Lock()
		old = append(old, p.active)
		p.mu.Unlock()
	}
	relay := peers[0].relayPeer()
	relay.mu.Lock()
	ready := relay.ready
	relay.mu.Unlock()
	relay.restartWithRoundFrom(ready, ready.RelayRound, ready.Phase, "turn_refresh", nil)
	for i, e := range engines {
		r := peers[i].relayPeer()
		waitSnapshotWithin(t, e, 3*time.Second, func(Snapshot) bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.ready.TransportGeneration != ready.TransportGeneration && r.pending != nil
		})
		peers[i].mu.Lock()
		retained := peers[i].active == old[i]
		peers[i].mu.Unlock()
		if !retained || old[i].mux.ctx.Err() != nil {
			t.Fatal("healthy TURN path closed while replacement was waiting for allocation")
		}
	}
	for _, release := range releases {
		release()
	}
	for i, e := range engines {
		p := peers[i]
		waitSnapshotWithin(t, e, 5*time.Second, func(Snapshot) bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.active != nil && p.active != old[i] && p.active.mux.ctx.Err() == nil && old[i].mux.ctx.Err() != nil
		})
	}
}

func TestParallelTURNReservationProgress(t *testing.T) {
	ca, pa := lifecycleCoordinator(t)
	cb, pb := lifecycleCoordinator(t)
	ca.desired.Config.DeviceName, cb.desired.Config.DeviceName = "alpha", "beta"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	setup := func(c *iceCoordinator, parent *icePeer) *icePeer {
		c.desired.Config = c.desired.Config.normalized()
		c.desired.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{"turn:127.0.0.1:3478?transport=udp"}, Username: "u", Credential: "p"}
		c.desired.Config.ICE.STUNURLs = []string{}
		c.platform = &credentialGatePlatform{calls: make(chan string, 32)}
		p := &icePeer{coordinator: c, ctx: ctx, online: true, activation: parent.activationMutex(), recorder: parent.processRecorder()}
		p.parallelParent.Store(parent)
		return p
	}
	ra, rb := setup(ca, pa), setup(cb, pb)
	attempt := func(ready iceSignalMessage) *iceAttempt {
		ac, stop := context.WithCancel(ctx)
		return &iceAttempt{ctx: ac, cancel: stop, ready: ready, events: make(chan iceSignalMessage, 16), failed: make(chan error, 1)}
	}
	pa.ready.InitiatorID, pa.ready.PeerDevice = "alpha", "beta"
	pb.ready.InitiatorID, pb.ready.PeerDevice = "alpha", "alpha"
	aa, ab := attempt(pa.ready), attempt(pb.ready)
	for _, p := range []*icePeer{ra, rb} {
		p.ready = pa.ready
		p.ready.Phase, p.ready.TransportGeneration = "relay_udp", 2
		p.pending = attempt(p.ready)
	}
	rb.ready.PeerDevice = "alpha"
	rb.pending.ready = rb.ready
	startRelay := func(p *icePeer) {
		done := make(chan struct{})
		a := p.pending
		go func() { defer close(done); p.attempt(a) }()
		t.Cleanup(func() { cancel(); <-done })
	}
	next := func(c *iceCoordinator, want func(iceSignalMessage) bool) iceSignalMessage {
		t.Helper()
		for {
			select {
			case m := <-c.sink.messages:
				if want(m) {
					return m
				}
			case <-ctx.Done():
				t.Fatal("relay handshake blocked bilateral punch reservation")
			}
		}
	}
	reserve := func(p *icePeer, a *iceAttempt) {
		done := make(chan error, 1)
		go func() {
			release, err := p.reserveNATPunch(a)
			if release != nil {
				release()
			}
			done <- err
		}()
		t.Cleanup(func() { cancel(); <-done })
	}
	// B waits for A's relay while A reserves its allocator for punching.
	startRelay(rb)
	next(cb, func(m iceSignalMessage) bool { return m.EndOfCandidates })
	reserve(pa, aa)
	ab.events <- next(ca, func(m iceSignalMessage) bool { return m.Type == "nat_punch" && m.Punch.Kind == "reserve" })
	reserve(pb, ab)
	startRelay(ra)
	aa.events <- next(cb, func(m iceSignalMessage) bool { return m.Type == "nat_punch" && m.Punch.Kind == "reserved" })
}

func TestICEAllocationProtectsNewDestinations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	n := &iceNetwork{ctx: ctx, allocation: &natAllocationGate{}}
	conn := n.guardAllocation(pc).(transport.UDPConn)
	defer conn.Close()
	known := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	if _, err := conn.WriteTo([]byte("first"), known); err != nil {
		t.Fatal(err)
	}
	release, err := n.allocation.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan error, 1)
	go func() { _, err := conn.WriteToUDP([]byte("new"), &net.UDPAddr{IP: known.IP, Port: 10}); done <- err }()
	select {
	case err := <-done:
		t.Fatal("new endpoint bypassed the prediction reservation", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, _, err := conn.WriteMsgUDP([]byte("keepalive"), nil, known); err != nil {
		t.Fatal("known endpoint waited for prediction", err)
	}
	conn.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("closing the socket did not cancel its allocation wait", err)
		}
	case <-time.After(time.Second):
		t.Fatal("allocation wait survived socket close")
	}
}

func TestParallelDirectPromotionPreservesMuxAndRejectsOldCandidate(t *testing.T) {
	c, parent := lifecycleCoordinator(t)
	parent.ready.Profile, parent.ready.ParallelStart, parent.ready.PathUpgrade = ProfileICE, true, true
	parent.ready.CoreVersion, parent.ready.PeerCoreVersion, parent.ready.WorkerVersion = CoreVersion, CoreVersion, CoreVersion
	parent.ready.Mappings = nil
	parent.ready.TransportGeneration, parent.ready.UpgradeProbe = 4, true
	parent.generationFloor = 4
	old := parent.ready
	ctx, cancel := context.WithCancel(c.ctx)
	parent.pending = &iceAttempt{ctx: ctx, cancel: cancel, ready: old}
	ready := old
	ready.TransportGeneration, ready.Phase = 3, "relay_udp"
	ready.UpgradeProbe = false
	relay := newICEPeerWithParent(c, ready, parent)
	mux := &muxPeer{ctx: relay.ctx, owner: relay}
	live := &liveICETransport{ready: ready, pathType: "direct", mux: mux}
	relay.mu.Lock()
	relay.ready, relay.online, relay.active = ready, true, live
	relay.mu.Unlock()
	parent.parallelRelay, parent.active = relay, live
	t.Cleanup(func() {
		relay.mu.Lock()
		relay.active = nil // the synthetic mux has no underlying QUIC connection
		relay.mu.Unlock()
		relay.close()
	})
	recorder := parent.processRecorder()
	activation := parent.activationMutex()
	ready.Phase = "direct"
	got := c.promoteParallelDirect(parent, ready)
	got.update(ready)
	if got != relay || c.peerList()[0] != relay || !parent.closed || ctx.Err() == nil {
		t.Fatal("the other startup attempt was not retired")
	}
	if relay.parallelParent.Load() != nil || relay.active != live || !relay.selectedMux(mux) || mux.ctx.Err() != nil {
		t.Fatal("promotion replaced the selected direct mux")
	}
	if relay.activationMutex() != activation || relay.processRecorder() != recorder {
		t.Fatal("promotion replaced sender synchronization or its process history")
	}
	if c.signalNeeded() {
		t.Fatal("completed direct startup still requires signaling")
	}
	old.ParallelRelay = &ready
	relay.update(old)
	if relay.ready.TransportGeneration != ready.TransportGeneration || relay.relayPeer() != nil || relay.pending != nil {
		t.Fatal("a higher but discarded candidate revived startup")
	}
	relay.update(ready)
	if relay.active != live || relay.pending != nil {
		t.Fatal("replaying the adopted generation replaced the direct path")
	}
}
