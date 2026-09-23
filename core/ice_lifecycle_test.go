package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func lifecycleCoordinator(t *testing.T) (*iceCoordinator, *icePeer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	identity, err := makeTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	r := engineRequest()
	r.Config.Transport.Preferred = PreferredICE
	r.Config.TURN.Mode = "worker"
	c := newICECoordinator(ctx, r, DefaultPlatform{}, nil, identity, strings.Repeat("a", 32), 1, func(Event) {}, func(NetworkSnapshot) {})
	c.online, c.serverV2, c.leaseRenewal = true, true, true
	c.sink = &iceSignalSink{ctx: ctx, messages: make(chan iceSignalMessage, 16)}
	p := &icePeer{coordinator: c, ctx: ctx, cancel: func() {}, online: true,
		ready: iceSignalMessage{SignalMessage: SignalMessage{PeerDevice: "peer"}, TransportID: "pair", TransportGeneration: 1, Phase: "direct", LeaseUntil: time.Now().Add(10 * time.Minute).UnixMilli(), Mappings: []peerMappingRecord{{ID: "test"}}}}
	c.peers["pair"] = p
	return c, p
}

func TestLeaseRenewalIsLightweightAndSkipsIdleOrOfflinePeers(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	m, ok := c.leaseRequest()
	if !ok || m.Type != "transport_renew" {
		t.Fatal(m, ok)
	}
	c.leaseRenewal = false
	m, ok = c.leaseRequest()
	if !ok || m.Type != "transport_sync" {
		t.Fatal("old Worker compatibility", m, ok)
	}
	p.online = false
	if _, ok = c.leaseRequest(); ok {
		t.Fatal("offline peer generated lease traffic")
	}
	p.online = true
	p.ready.Mappings = nil
	if _, ok = c.leaseRequest(); ok {
		t.Fatal("empty relationship generated lease traffic")
	}
	delete(c.peers, "pair")
	if _, ok = c.leaseRequest(); ok {
		t.Fatal("idle client generated lease traffic")
	}
}

func TestLeaseReplyCannotReviveStaleGenerationOrOfflinePeer(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	old := p.ready.LeaseUntil
	p.renewLease(transportLease{Generation: 2, Until: old + 1000})
	p.renewLease(transportLease{Generation: 1, Until: old - 1000})
	if p.ready.LeaseUntil != old {
		t.Fatal("stale lease accepted")
	}
	p.renewLease(transportLease{Generation: 1, Until: old + 1000})
	if p.ready.LeaseUntil != old+1000 {
		t.Fatal("lease not renewed")
	}
	p.online = false
	p.renewLease(transportLease{Generation: 1, Until: old + 2000})
	if p.ready.LeaseUntil != old+1000 || p.allowNew() {
		t.Fatal("offline peer regained authorization")
	}
}

func TestTURNRequestedOnlyForPendingOrActiveLocalRelay(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	p.pending = &iceAttempt{ready: iceSignalMessage{Phase: "direct"}}
	c.requestTURN()
	if len(c.sink.messages) != 0 {
		t.Fatal("direct probe requested credentials")
	}
	p.pending = nil
	p.active = &liveICETransport{expires: 0}
	c.requestTURN()
	if len(c.sink.messages) != 0 {
		t.Fatal("healthy direct path requested credentials")
	}
	p.pending = &iceAttempt{ready: iceSignalMessage{Phase: "relay_udp"}}
	c.requestTURN()
	c.requestTURN()
	if len(c.sink.messages) != 1 || c.turnRequestID == "" {
		t.Fatal("relay request not coalesced")
	}
	m := <-c.sink.messages
	if m.Type != "turn_request" {
		t.Fatal(m)
	}
	c.turnRequestID = ""
	c.turnRefresh = time.Now().Add(time.Hour).UnixMilli()
	c.turnExpires = c.turnRefresh + 1000
	c.requestTURN()
	if len(c.sink.messages) != 0 {
		t.Fatal("valid credentials refreshed early")
	}
	p.pending = nil
	p.active.expires = time.Now().Add(time.Minute).UnixMilli()
	c.turnRefresh = 0
	c.requestTURN()
	if len(c.sink.messages) != 1 {
		t.Fatal("local relay did not refresh credentials")
	}
}

func TestTURNFailuresReleaseInflightAndBackOff(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	p.pending = &iceAttempt{ready: iceSignalMessage{Phase: "relay_udp"}}
	c.requestTURN()
	first := <-c.sink.messages
	if !c.failTURNRequest(iceSignalMessage{SignalMessage: SignalMessage{Type: "error", Code: "transport_not_authorized", Message: "peer offline"}, RequestID: first.RequestID}) {
		t.Fatal("generic failure left request pending")
	}
	if c.turnRequestID != "" || c.turnBackoff != 30*time.Second {
		t.Fatal(c.turnRequestID, c.turnBackoff)
	}
	c.requestTURN()
	if len(c.sink.messages) != 0 {
		t.Fatal("ignored backoff")
	}
	c.turnRetry = time.Now().Add(-time.Second)
	c.requestTURN()
	second := <-c.sink.messages
	c.failTURNRequest(iceSignalMessage{SignalMessage: SignalMessage{Type: "error", Code: "relay_unavailable"}, RequestID: second.RequestID})
	if c.turnBackoff != time.Minute {
		t.Fatal(c.turnBackoff)
	}
	for i := 0; i < 10; i++ {
		c.turnMu.Lock()
		c.turnFailedLocked(time.Now())
		c.turnMu.Unlock()
	}
	if c.turnBackoff != 5*time.Minute {
		t.Fatal("unbounded backoff", c.turnBackoff)
	}
}

func TestTURNMissingReplyTimesOutInsteadOfStickingRequesting(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	p.pending = &iceAttempt{ready: iceSignalMessage{Phase: "relay_udp"}}
	c.turnRequestID = "lost"
	c.turnLast = time.Now().Add(-16 * time.Second)
	c.turnState = "requesting"
	c.requestTURN()
	if c.turnRequestID != "" || c.turnState != "unavailable" || !c.turnRetry.After(time.Now()) {
		t.Fatal("request stuck", c.turnState)
	}
}

func TestOnDemandTURNWaitIsCancelableAndUsesOneGeneration(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	p.pending = &iceAttempt{ready: iceSignalMessage{Phase: "relay_udp"}}
	ctx, cancel := context.WithCancel(c.ctx)
	result := make(chan error, 1)
	go func() { _, _, _, err := c.relayServers(ctx); result <- err }()
	var request iceSignalMessage
	select {
	case request = <-c.sink.messages:
	case <-time.After(time.Second):
		t.Fatal("no credential request")
	}
	response := iceSignalMessage{SignalMessage: SignalMessage{Type: "turn_servers"}, RequestID: request.RequestID, ICEServers: []ICEServer{{URLs: []string{"turn:HOST:3478?transport=udp"}}}, ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), RefreshAt: time.Now().Add(45 * time.Minute).UnixMilli()}
	if err := c.handle(response, c.request(), DefaultPlatform{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("credential reply did not wake waiter")
	}
	if len(c.sink.messages) != 0 {
		t.Fatal("credential arrival restarted transport")
	}
	cancel()
	c.turnMu.Lock()
	c.turnServers = nil
	c.turnRefresh = 0
	c.turnMu.Unlock()
	ctx, cancel = context.WithCancel(c.ctx)
	cancel()
	_, _, _, err := c.relayServers(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestManualRenominationOnlyRequestsEligibleActiveTransport(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	c.renominate()
	select {
	case m := <-c.sink.messages:
		t.Fatalf("inactive transport requested restart: %+v", m)
	default:
	}

	p.active = &liveICETransport{ready: iceSignalMessage{Phase: "direct"}}
	var group sync.WaitGroup
	group.Add(4)
	for range 4 {
		go func() {
			defer group.Done()
			c.renominate()
		}()
	}
	group.Wait()
	select {
	case m := <-c.sink.messages:
		if m.Type != "transport_restart" || m.TransportID != "pair" || m.ExpectedGeneration != 1 || m.Phase != "direct" || m.Reason != "manual_renomination" {
			t.Fatalf("unexpected renomination request: %+v", m)
		}
	default:
		t.Fatal("active transport did not request renomination")
	}

	c.renominate()
	if len(c.sink.messages) != 0 {
		t.Fatalf("duplicate request not suppressed: %+v", c.sink.messages)
	}

	p.requestedGeneration = 0
	p.pending = &iceAttempt{}
	c.renominate()
	if len(c.sink.messages) != 0 {
		t.Fatalf("pending transport requested restart: %+v", c.sink.messages)
	}

	p.pending = nil
	c.renominate()
	p.pending = &iceAttempt{}
	c.renominate()
	if len(c.sink.messages) != 1 {
		t.Fatalf("pending arrival did not suppress a second request: %+v", c.sink.messages)
	}
}
