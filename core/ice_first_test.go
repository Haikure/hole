package core

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDirectICEPrecedesEveryNATClassification(t *testing.T) {
	for _, kind := range []string{"probing", "unknown", "open", "firewall", "full_cone", "restricted_cone", "port_restricted_cone", "symmetric"} {
		t.Run(kind, func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			c.desired.Config = c.desired.Config.normalized()
			c.desired.Config.ICE.STUNURLs = []string{}
			c.desired.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478"}
			c.platform = &credentialGatePlatform{calls: make(chan string, 16)}
			c.natSnapshot = NATSnapshot{Status: "complete", Type: kind, Predictability: "irregular", Epoch: c.networkEpoch.Load(), ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			if kind == "probing" {
				c.natSnapshot.Status, c.natSnapshot.ProbedAt = "probing", ""
			}
			p.ready.PunchVersion, p.ready.DirectOrder, p.ready.DirectStage = natPunchVersion, directOrderICEFirst, "ice"
			ctx, cancel := context.WithCancel(c.ctx)
			a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready, events: make(chan iceSignalMessage, 8), failed: make(chan error, 1)}
			p.pending = a
			done := make(chan struct{})
			go func() { defer close(done); p.attempt(a) }()
			t.Cleanup(func() { cancel(); <-done })
			select {
			case m := <-c.sink.messages:
				if m.Type != "ice_description" {
					t.Fatalf("first signal = %s, want ICE before NAT negotiation", m.Type)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("ICE waited for NAT classification")
			}
			a.failed <- errors.New("fixture ICE failure")
			deadline := time.After(2 * time.Second)
			for {
				select {
				case m := <-c.sink.messages:
					if m.Type == "nat_punch" {
						t.Fatal("NAT negotiation reused the ICE generation")
					}
					if m.Type == "transport_restart" {
						if m.Reason != "ice_failed" || m.ExpectedGeneration != a.ready.TransportGeneration {
							t.Fatalf("unexpected fallback: %+v", m)
						}
						return
					}
				case <-deadline:
					t.Fatal("ICE failure did not request NAT fallback")
				}
			}
		})
	}
}

type pendingNATPlatform struct{ DefaultPlatform }

func (p pendingNATPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	if network == "udp4" && address == "0.0.0.0:0" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.DefaultPlatform.ListenPacket(ctx, network, address)
}

func TestDirectICESucceedsWhileNATClassificationIsPending(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	a, b := NewEngine(Options{Platform: pendingNATPlatform{}}), NewEngine(Options{Platform: pendingNATPlatform{}})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	for _, r := range []*Request{&ra, &rb} {
		r.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478"}
		r.Config.ICE.PortMapping = "off"
	}
	ra.Config.Provide = []Provide{{ID: "first", Service: ServiceEndpoint{"tcp", "127.0.0.1", unusedTCPPort(t)}}}
	rb.Config.Consume = []Consume{{ID: "first", Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []*Engine{a, b} {
		s := waitSnapshotWithin(t, engine, 5*time.Second, func(s Snapshot) bool {
			return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.Network.NAT.Status == "probing"
		})
		p := s.PeerTransports[0]
		if p.Generation != 1 || p.PathType != "direct" || p.LocalCandidates == 0 {
			t.Fatalf("expected initial ICE success: %+v", p)
		}
	}
}

func TestOldWorkerOrdinaryICEOfferDoesNotWaitForNAT(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	a := &iceAttempt{ready: p.ready}
	if err := p.offerOrdinaryICE(a, c.desired.Config); err != nil {
		t.Fatal(err)
	}
	m := <-c.sink.messages
	if m.Type != "nat_punch" || m.Punch.Kind != "offer" || m.Punch.Mode != "disabled" || m.Punch.Snapshot.ProbedAt != "" || len(m.Punch.Nonce) != 64 || m.Punch.Config.validate() != nil {
		t.Fatalf("invalid compatibility offer: %+v", m.Punch)
	}
}

func TestNATFallbackSkipsUnsupportedOrDisabledPunching(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpredictable", true: "disabled"}[disabled], func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			c.desired.Config = c.desired.Config.normalized()
			c.desired.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478"}
			if disabled {
				c.desired.Config.ICE.NATSTUNURLs = []string{}
			}
			c.natSnapshot = NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", Predictability: "irregular", Epoch: c.networkEpoch.Load(), ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			p.ready.PunchVersion, p.ready.DirectOrder, p.ready.DirectStage = natPunchVersion, directOrderICEFirst, "punch"
			ctx, cancel := context.WithCancel(c.ctx)
			defer cancel()
			a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready, events: make(chan iceSignalMessage, 1), failed: make(chan error, 1)}
			p.pending = a
			peer := signalMessage("nat_punch")
			peer.Punch = &natPunchSignal{Kind: "offer", Nonce: strings.Repeat("a", 64), Snapshot: &c.natSnapshot, Config: &c.desired.Config.ICE.Punch}
			a.events <- peer
			p.attempt(a)
			if m := <-c.sink.messages; m.Type != "nat_punch" || m.Punch.Kind != "offer" {
				t.Fatalf("fallback did not negotiate NAT: %+v", m)
			}
			select {
			case m := <-c.sink.messages:
				if m.Type != "transport_restart" || m.Reason != "direct_exhausted" {
					t.Fatalf("unsupported punch repeated ICE: %+v", m)
				}
			default:
				t.Fatal("unsupported punch did not advance to relay")
			}
		})
	}
}
