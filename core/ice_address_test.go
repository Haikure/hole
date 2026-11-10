package core

import (
	"context"
	"reflect"
	"testing"
)

func TestICEPeerObservation(t *testing.T) {
	for _, tc := range []struct {
		name, family, localType, local, observed, want, wantType string
	}{
		{"host_nat", "IPv4", "host", "10.0.0.7:23456", "203.0.113.7:54321", "203.0.113.7:54321", "prflx"},
		{"endpoint_dependent", "IPv4", "srflx", "203.0.113.7:23456", "203.0.113.7:54321", "203.0.113.7:54321", "prflx"},
		{"mapped_ipv4", "IPv4", "host", "10.0.0.7:23456", "[::ffff:203.0.113.7]:54321", "203.0.113.7:54321", "prflx"},
		{"lan", "IPv4", "host", "10.0.0.7:23456", "10.0.0.7:23456", "10.0.0.7:23456", "host"},
		{"same_srflx", "IPv4", "srflx", "203.0.113.7:54321", "203.0.113.7:54321", "203.0.113.7:54321", "srflx"},
		{"ipv6", "IPv6", "host", "[fd00::7]:23456", "[2001:db8::7]:54321", "[2001:db8::7]:54321", "prflx"},
		{"local_relay", "IPv4", "relay", "192.0.2.1:45000", "203.0.113.7:54321", "192.0.2.1:45000", "relay"},
		{"legacy", "IPv4", "host", "10.0.0.7:23456", "", "10.0.0.7:23456", "host"},
		{"wrong_family", "IPv4", "host", "10.0.0.7:23456", "[2001:db8::7]:54321", "10.0.0.7:23456", "host"},
		{"wrong_family_ipv6", "IPv6", "host", "[fd00::7]:23456", "203.0.113.7:54321", "[fd00::7]:23456", "host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := PeerTransportSnapshot{LocalType: tc.localType, LocalAddress: tc.local, AddressFamily: tc.family,
				PathType: "relay", RelaySide: "remote", RemoteType: "relay", RemoteAddress: "192.0.2.2:45000", RemoteRelayProtocol: "tls"}
			want := s
			want.LocalAddress, want.LocalType = tc.want, tc.wantType
			applyICEPeerObservation(&s, tc.observed)
			if !reflect.DeepEqual(s, want) {
				t.Fatalf("unexpected path display: %+v, want %+v", s, want)
			}
		})
	}
	for _, observed := range []string{"0.0.0.0:1234", "224.0.0.1:1234", "255.255.255.255:1234", "example.com:1234", "203.0.113.7:0", "203.0.113.7:65536", "[::]:1234", "[ff02::1]:1234", "[fe80::1%eth0]:1234"} {
		for _, family := range []string{"IPv4", "IPv6"} {
			if got := observedPathAddress(observed, family); got != "" {
				t.Fatalf("invalid observation %q accepted for %s: %q", observed, family, got)
			}
		}
	}
}

func TestICEPeerObservationSurvivesSnapshotRefresh(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	a, b := NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	ra.Config.Provide = []Provide{{ID: "snapshot", Service: ServiceEndpoint{"tcp", "127.0.0.1", 9}}}
	rb.Config.Consume = []Consume{{ID: "snapshot", Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active"
	})
	a.mu.Lock()
	coordinator := a.run.ice
	a.mu.Unlock()
	peer := coordinator.peerList()[0]
	peer.mu.Lock()
	live, stats := peer.active, peer.stats
	peer.mu.Unlock()
	if live.agent == nil || live.mux.observedLocalAddress == "" {
		t.Fatal("ordinary ICE did not exchange the peer observation")
	}
	pair, err := live.agent.GetSelectedCandidatePair()
	if err != nil || pair == nil {
		t.Fatalf("missing selected pair: %v", err)
	}
	// Use the real selected pair but a different authenticated observation to
	// model a NAT mapping. A separate view avoids mutating the live mux.
	observed := "203.0.113.7:54321"
	if stats.AddressFamily == "IPv6" {
		observed = "[2001:db8::7]:54321"
	}
	view := &icePeer{coordinator: coordinator, online: true, stats: stats,
		active: &liveICETransport{ready: live.ready, agent: live.agent,
			mux: &muxPeer{ctx: context.Background(), observedLocalAddress: observed}}}
	for range 3 {
		s := view.snapshot()
		if s.LocalAddress != observed || s.LocalType != "prflx" || s.RemoteAddress != candidateAddress(pair.Remote) {
			t.Fatalf("candidate refresh lost peer observation: %+v", s)
		}
		for _, candidate := range s.CandidatePairs {
			if candidate.Selected && candidate.LocalAddress != candidateAddress(pair.Local) {
				t.Fatal("display update changed raw candidate diagnostics")
			}
		}
	}
	view.active.mux.observedLocalAddress = ""
	if got := view.snapshot().LocalAddress; got != candidateAddress(pair.Local) {
		t.Fatalf("old peer fallback = %q, want selected candidate", got)
	}
}
