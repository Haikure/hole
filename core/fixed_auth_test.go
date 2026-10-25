package core

import (
	"context"
	"crypto/tls"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestFixedSharedListenerAuthenticatesBeforeRouting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverIdentity, err := makeTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	clientIdentity, err := makeTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	wrongIdentity, err := makeTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	r := iceFixtureRequest("ws://127.0.0.1:1", "alpha")
	r.Config.Transport.FixedEndpoint = fixedEndpoint(t, "udp4")
	runtime, remoteRuntime := strings.Repeat("a", 32), strings.Repeat("b", 32)
	c := newICECoordinator(ctx, r, DefaultPlatform{}, nil, serverIdentity, runtime, 1, func(Event) {}, func(NetworkSnapshot) {})
	defer c.close()
	if err := c.configureFixed(r); err != nil {
		t.Fatal(err)
	}
	m := iceSignalMessage{SignalMessage: SignalMessage{PeerDevice: "beta", PeerFingerprint: certFingerprint(clientIdentity.Certificates[0])}, Profile: ProfileFixed, TransportID: "authorized", TransportGeneration: 7, RuntimeID: runtime, PeerRuntimeID: remoteRuntime, ListenerID: "alpha", DialerID: "beta", InitiatorID: "beta", LeaseUntil: time.Now().Add(time.Minute).UnixMilli()}
	attemptCtx, stopAttempt := context.WithCancel(c.ctx)
	defer stopAttempt()
	a := &iceAttempt{ctx: attemptCtx, cancel: stopAttempt, ready: m, incoming: make(chan *fixedIncoming, 1)}
	p := &icePeer{coordinator: c, ctx: attemptCtx, cancel: stopAttempt, ready: m, pending: a, online: true}
	c.mu.Lock()
	c.peers[m.TransportID] = p
	c.online = true
	c.mu.Unlock()
	hello := muxHello{Profile: ProfileFixed, TransportID: m.TransportID, Generation: m.TransportGeneration, LocalRuntime: remoteRuntime, RemoteRuntime: runtime, Version: sessionProtocol}
	for _, tc := range []struct {
		name     string
		change   func(*muxHello)
		identity *tls.Config
		valid    bool
	}{
		{"unknown transport", func(h *muxHello) { h.TransportID = "unknown" }, clientIdentity, false},
		{"stale generation", func(h *muxHello) { h.Generation-- }, clientIdentity, false},
		{"wrong runtime", func(h *muxHello) { h.LocalRuntime = strings.Repeat("c", 32) }, clientIdentity, false},
		{"wrong recipient", func(h *muxHello) { h.RemoteRuntime = strings.Repeat("c", 32) }, clientIdentity, false},
		{"wrong profile", func(h *muxHello) { h.Profile = ProfileICE }, clientIdentity, false},
		{"wrong certificate", func(*muxHello) {}, wrongIdentity, false},
		{"authorized", func(*muxHello) {}, clientIdentity, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			tlsConfig := tc.identity.Clone()
			tlsConfig.NextProtos = []string{fixedALPN}
			tlsConfig.InsecureSkipVerify = true
			conn, err := quic.DialAddr(hsCtx, r.Config.Transport.FixedEndpoint.Advertise, tlsConfig, iceQUICConfig("direct"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseWithError(0, "test done")
			stream, err := conn.OpenStreamSync(hsCtx)
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.SetDeadline(time.Now().Add(3 * time.Second))
			h := hello
			tc.change(&h)
			if err := writeMuxPrefix(stream, muxControl, 0); err != nil {
				t.Fatal(err)
			}
			if err := writeSessionJSON(stream, h); err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				select {
				case incoming := <-a.incoming:
					if incoming.hello.Generation != m.TransportGeneration {
						t.Fatal("wrong generation routed")
					}
					_ = incoming.conn.CloseWithError(0, "test done")
				case <-hsCtx.Done():
					t.Fatal("authorized handshake not routed")
				}
			} else {
				var response muxHello
				if err := readSessionJSON(stream, &response); err == nil {
					t.Fatal("unauthorized peer accepted")
				}
				select {
				case <-a.incoming:
					t.Fatal("unauthorized peer reached mux")
				default:
				}
			}
		})
	}
}

func TestFixedReadyRejectsUnauthorizedMappingsAndPolicies(t *testing.T) {
	r := iceFixtureRequest("ws://127.0.0.1:1", "alpha")
	r.Config.Transport.FixedEndpoint = &FixedEndpointConfig{"0.0.0.0:40000", "gateway.example:50000"}
	r.Config.Provide = []Provide{{"ssh", ServiceEndpoint{"tcp", "127.0.0.1", 22}}}
	c := &iceCoordinator{desired: r, runtimeID: "local-runtime"}
	record := peerMappingRecord{Key: "authorized-key", ID: "ssh", Provider: "alpha", Consumer: "beta", Service: r.Config.Provide[0].Service}
	m := iceSignalMessage{SignalMessage: SignalMessage{PeerDevice: "beta"}, Profile: ProfileFixed, Phase: "fixed", RuntimeID: c.runtimeID, FixedEndpoint: r.Config.Transport.FixedEndpoint.Advertise, ListenerID: "alpha", DialerID: "beta", Mappings: []peerMappingRecord{record}}
	if err := c.validateFixedReady(m); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*iceSignalMessage){
		func(m *iceSignalMessage) { m.FixedEndpoint = "other.example:50000" },
		func(m *iceSignalMessage) { m.ListenerID = "third-device" },
		func(m *iceSignalMessage) { m.Phase = "direct" },
		func(m *iceSignalMessage) { m.RelayRequired = true },
		func(m *iceSignalMessage) { m.RuntimeID = "stale-runtime" },
		func(m *iceSignalMessage) { m.RetryAfterMS = 15001 },
		func(m *iceSignalMessage) { m.Mappings[0].Service.Port = 23 },
		func(m *iceSignalMessage) { m.Mappings[0].Consumer = "third-device" },
		func(m *iceSignalMessage) { m.Mappings = nil },
	} {
		copy := m
		copy.Mappings = append([]peerMappingRecord{}, m.Mappings...)
		mutate(&copy)
		if err := c.validateFixedReady(copy); err == nil {
			t.Fatalf("invalid authorization accepted: %+v", copy)
		}
	}
}
