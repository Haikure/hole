package core

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func liveTestEcho(t *testing.T) ServiceEndpoint {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	return ServiceEndpoint{"tcp", "127.0.0.1", l.Addr().(*net.TCPAddr).Port}
}

func liveTestPath(t *testing.T, e *Engine) (*liveICETransport, *iceSignalSink) {
	t.Helper()
	e.mu.Lock()
	c := e.run.ice
	e.mu.Unlock()
	peers := c.peerList()
	if len(peers) != 1 {
		t.Fatalf("unexpected peers: %d", len(peers))
	}
	peers[0].mu.Lock()
	path := peers[0].active
	peers[0].mu.Unlock()
	c.mu.Lock()
	sink := c.sink
	c.mu.Unlock()
	return path, sink
}

func TestLiveConfigurationKeepsQUICAndSignal(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	target, alternate := liveTestEcho(t), liveTestEcho(t)
	a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	ra.Config.Provide = []Provide{{ID: "keep", Service: target}, {ID: "edit", Service: target}}
	rb.Config.Consume = []Consume{{ID: "keep", Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	wait := func(channels int) {
		t.Helper()
		for _, e := range []*Engine{a, b} {
			waitSnapshot(t, e, func(s Snapshot) bool {
				if s.EngineState != "running" || s.SignalState != "joined" || len(s.PeerTransports) != 1 || s.PeerTransports[0].State != "active" || s.PeerTransports[0].ActiveChannels != channels {
					return false
				}
				path, _ := liveTestPath(t, e)
				for _, record := range path.mux.owner.records() {
					for _, engine := range []*Engine{a, b} {
						engine.mu.Lock()
						coordinator := engine.run.ice
						engine.mu.Unlock()
						if coordinator == nil || !coordinator.allowed(record) {
							return false
						}
					}
					path.mux.mu.Lock()
					channel := path.mux.byMapping[record.ID]
					current := channel != nil && channel.mapping == record && channel.ready.Load()
					path.mux.mu.Unlock()
					if !current {
						return false
					}
				}
				return true
			})
		}
	}
	wait(1)
	pathA, sinkA := liveTestPath(t, a)
	pathB, sinkB := liveTestPath(t, b)
	if !pathA.mux.liveConfig || !pathB.mux.liveConfig {
		t.Fatal("live capability was not negotiated")
	}
	conn, err := net.Dial("tcp4", endpointString("127.0.0.1", rb.Config.Consume[0].Expose.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Continuous traffic exposes a hidden transport replacement even when the
	// application replay layer successfully masks it from a one-shot transfer.
	trafficCtx, stopTraffic := context.WithCancel(context.Background())
	trafficDone := make(chan error, 1)
	go func() {
		for trafficCtx.Err() == nil {
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write([]byte("keep")); err != nil {
				trafficDone <- err
				return
			}
			var data [4]byte
			if _, err := io.ReadFull(conn, data[:]); err != nil {
				trafficDone <- err
				return
			}
			if string(data[:]) != "keep" {
				trafficDone <- io.ErrUnexpectedEOF
				return
			}
		}
		trafficDone <- nil
	}()
	defer stopTraffic()
	apply := func(e *Engine, r Request, channels int) {
		t.Helper()
		if err := e.ApplyConfig(r); err != nil {
			t.Fatal(err)
		}
		wait(channels)
		for _, item := range []struct {
			e    *Engine
			path *liveICETransport
			sink *iceSignalSink
		}{{a, pathA, sinkA}, {b, pathB, sinkB}} {
			path, sink := liveTestPath(t, item.e)
			if path != item.path || sink != item.sink || path.session.Context().Err() != nil {
				t.Fatal("live update replaced QUIC or WebSocket")
			}
		}
	}
	rb.Config.SessionTimeout = 93 * time.Second
	apply(b, rb, 1)
	rb.Config.ICE.GatherTimeout = ConfigDuration(4 * time.Second)
	rb.Config.ICE.STUNURLs = []string{"stun:127.0.0.1:9"}
	apply(b, rb, 1)
	rb.Config.Consume = append(rb.Config.Consume, Consume{ID: "edit", Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}})
	apply(b, rb, 2)
	checkEdited := func() net.Conn {
		t.Helper()
		client, err := net.Dial("tcp4", endpointString("127.0.0.1", rb.Config.Consume[1].Expose.Port))
		if err != nil {
			t.Fatal(err)
		}
		_ = client.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err = client.Write([]byte("edit")); err != nil {
			t.Fatal(err)
		}
		var data [4]byte
		if _, err = io.ReadFull(client, data[:]); err != nil || string(data[:]) != "edit" {
			t.Fatalf("edited mapping: %q %v", data, err)
		}
		return client
	}
	edited := checkEdited()
	ra.Config.Provide[1].Service = alternate
	apply(a, ra, 2)
	_ = edited.SetReadDeadline(time.Now().Add(time.Second))
	var one [1]byte
	if _, err := edited.Read(one[:]); err == nil {
		t.Fatal("retargeted mapping kept old application session")
	}
	edited.Close()
	checkEdited().Close()
	oldPort := rb.Config.Consume[1].Expose.Port
	rb.Config.Consume[1].Expose.Port = unusedTCPPort(t)
	apply(b, rb, 2)
	if stale, err := net.DialTimeout("tcp4", endpointString("127.0.0.1", oldPort), time.Second); err == nil {
		stale.Close()
		t.Fatal("old listener survived")
	}
	checkEdited().Close()
	udpEcho, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go func() {
		var data [128]byte
		for {
			n, addr, err := udpEcho.ReadFrom(data[:])
			if err != nil {
				return
			}
			_, _ = udpEcho.WriteTo(data[:n], addr)
		}
	}()
	ra.Config.Provide[1].Service = ServiceEndpoint{"udp", "127.0.0.1", udpEcho.LocalAddr().(*net.UDPAddr).Port}
	apply(a, ra, 2)
	udpClient, err := net.Dial("udp4", endpointString("127.0.0.1", rb.Config.Consume[1].Expose.Port))
	if err != nil {
		t.Fatal(err)
	}
	_ = udpClient.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = udpClient.Write([]byte("udp"))
	if err != nil {
		t.Fatal(err)
	}
	var datagram [128]byte
	n, err := udpClient.Read(datagram[:])
	udpClient.Close()
	if err != nil || string(datagram[:n]) != "udp" {
		t.Fatalf("protocol change: %q %v", datagram[:n], err)
	}
	ra.Config.Provide[1].Service = alternate
	apply(a, ra, 2)
	checkEdited().Close()
	ra.Config.Voice.Enabled = true
	apply(a, ra, 2)
	for i := 0; i < 3; i++ {
		rb.Config.Voice.Enabled = true
		apply(b, rb, 2)
		for _, e := range []*Engine{a, b} {
			waitSnapshot(t, e, func(s Snapshot) bool {
				return len(s.Voice.Peers) == 1 && pathA.mux.voiceEnabled.Load() && pathB.mux.voiceEnabled.Load()
			})
		}
		if !pathA.mux.voiceEnabled.Load() || !pathB.mux.voiceEnabled.Load() {
			t.Fatal("voice not enabled on original QUIC")
		}
		if err := b.PushVoicePCM(0, 0, make([]byte, 1920)); err != nil {
			t.Fatal(err)
		}
		waitSnapshot(t, a, func(s Snapshot) bool { return len(s.Voice.Peers) == 1 && s.Voice.Peers[0].ReceivedFrames > 0 })
		rb.Config.Voice.Enabled = false
		apply(b, rb, 2)
		waitSnapshot(t, a, func(s Snapshot) bool { return len(s.Voice.Peers) == 0 })
	}
	// An edit and immediate undo must still leave the engine out of applying.
	ra.Config.SessionTimeout = 94 * time.Second
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	ra.Config.SessionTimeout = 0
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	wait(2)
	hibernate()
	rb.Config.Consume = rb.Config.Consume[:1]
	apply(b, rb, 1)
	stopTraffic()
	if err := <-trafficDone; err != nil {
		t.Fatal(err)
	}
}

func TestLiveConfigScopeAndPathClassification(t *testing.T) {
	base := engineRequest()
	for _, tc := range []struct {
		name string
		edit func(*Config)
		path bool
	}{
		{"timeout", func(c *Config) { c.SessionTimeout = time.Second }, false},
		{"STUN", func(c *Config) { c.ICE.STUNURLs = []string{"stun:127.0.0.1:9"} }, false},
		{"probe", func(c *Config) { c.ICE.DirectProbeTimeout = ConfigDuration(time.Second) }, false},
		{"relay-only", func(c *Config) { c.ICE.RelayOnly = !c.ICE.RelayOnly }, true},
		{"interface", func(c *Config) { c.ICE.InterfaceAllowlist = []string{"lo"} }, true},
		{"TURN", func(c *Config) { c.TURN.Credential = "changed" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.edit(&next.Config)
			if got := pathConfigChanged(base.Config, next.Config); got != tc.path {
				t.Fatalf("path changed=%v", got)
			}
		})
	}
	for _, edit := range []func(*Request){func(r *Request) { r.Config.Token = "new" }, func(r *Request) { r.Config.DeviceName = "new" }, func(r *Request) { r.Config.Room = "new" }, func(r *Request) { r.ServerURL = "wss://other.invalid" }} {
		next := base
		edit(&next)
		if sameScope(base, next) {
			t.Fatal("authentication edit allowed in place")
		}
	}
}

func TestLiveConfigCoalescesEditsAndRejectsStaleAcknowledgments(t *testing.T) {
	c, _ := lifecycleCoordinator(t)
	c.sessions = newSessionManager(time.Minute)
	defer c.sessions.close()
	c.desired.Config.Transport = TransportConfig{Preferred: PreferredICE}
	initial := c.request()
	incoming := make(chan iceSignalMessage, 16)
	result := make(chan error, 1)
	go func() {
		applied := initial
		err, _ := c.signalUpdates(c.ctx, incoming, make(chan error), DefaultPlatform{}, initial, &applied)
		result <- err
	}()
	defer func() { c.cancel(); <-result }()
	joined := signalMessage("joined")
	joined.SignalVersion = 2
	joined.LiveConfig = true
	incoming <- joined
	nextMessage := func(kind string) iceSignalMessage {
		t.Helper()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case m := <-c.sink.messages:
				if m.Type == kind {
					return m
				}
			case <-timer.C:
				t.Fatalf("missing %s", kind)
				return iceSignalMessage{}
			}
		}
	}
	nextMessage("transport_sync")
	first := initial
	first.Config.Provide = []Provide{{ID: "first", Service: ServiceEndpoint{"tcp", "127.0.0.1", 22}}}
	c.submit(first)
	one := nextMessage("member_update")
	latest := initial
	latest.Config.Provide = []Provide{{ID: "latest", Service: ServiceEndpoint{"tcp", "127.0.0.1", 23}}}
	c.submit(latest)
	wrong := signalMessage("member_updated")
	wrong.MemberRevision = one.MemberRevision
	wrong.RequestID = "wrong"
	incoming <- wrong
	select {
	case m := <-c.sink.messages:
		t.Fatalf("advanced before matching ACK: %+v", m)
	case <-time.After(30 * time.Millisecond):
	}
	wrong.RequestID = one.RequestID
	incoming <- wrong
	two := nextMessage("member_update")
	if two.MemberRevision <= one.MemberRevision || len(two.Provide) != 1 || two.Provide[0].ID != "latest" {
		t.Fatal(two)
	}
	incoming <- wrong // a duplicate ACK cannot acknowledge revision two
	select {
	case m := <-c.sink.messages:
		t.Fatalf("stale ACK advanced update: %+v", m)
	case <-time.After(30 * time.Millisecond):
	}
	ack := signalMessage("member_updated")
	ack.MemberRevision = two.MemberRevision
	ack.RequestID = two.RequestID
	incoming <- ack
	nextMessage("transport_sync")
}

func TestLiveConfigOldWorkerRejoinsOnlyForDeclarationChanges(t *testing.T) {
	c, _ := lifecycleCoordinator(t)
	c.sessions = newSessionManager(time.Minute)
	defer c.sessions.close()
	c.desired.Config.Transport = TransportConfig{Preferred: PreferredICE}
	initial := c.request()
	incoming := make(chan iceSignalMessage, 4)
	done := make(chan bool, 1)
	go func() {
		applied := initial
		_, changed := c.signalUpdates(c.ctx, incoming, make(chan error), DefaultPlatform{}, initial, &applied)
		done <- changed
	}()
	joined := signalMessage("joined")
	joined.SignalVersion = 2
	incoming <- joined
	select {
	case <-c.sink.messages:
	case <-time.After(time.Second):
		t.Fatal("join missing")
	}
	next := initial
	next.Config.SessionTimeout = 77 * time.Second
	c.submit(next)
	select {
	case <-done:
		t.Fatal("local timeout rejoined old Worker")
	case <-time.After(30 * time.Millisecond):
	}
	next.Config.Provide = []Provide{{ID: "new", Service: ServiceEndpoint{"tcp", "127.0.0.1", 22}}}
	c.submit(next)
	select {
	case changed := <-done:
		if !changed {
			t.Fatal("no compatibility rejoin")
		}
	case <-time.After(time.Second):
		c.cancel()
		<-done
		t.Fatal("old Worker fallback missing")
	}
}

func TestLiveVoiceDropsPreviousRevisionMedia(t *testing.T) {
	p := &muxPeer{liveConfig: true, voiceDatagrams: make(chan []byte, 2), voiceFeedback: make(chan []byte, 2)}
	p.voiceEnabled.Store(true)
	p.voiceRevision.Store(3)
	packet, _ := EncodeVoiceDatagram(0, 0, []byte("old"))
	wire := make([]byte, 12+len(packet))
	copy(wire, voiceEpochMagic[:])
	copy(wire[12:], packet)
	binary.BigEndian.PutUint64(wire[4:12], 1)
	p.receiveVoice(wire)
	p.receiveVoice(packet)
	if len(p.voiceDatagrams) != 0 {
		t.Fatal("old or unversioned media accepted")
	}
	binary.BigEndian.PutUint64(wire[4:12], 3)
	p.receiveVoice(wire)
	if len(p.voiceDatagrams) != 1 {
		t.Fatal("current media rejected")
	}
	p.voiceEnabled.Store(false)
	p.receiveVoice(wire)
	if len(p.voiceDatagrams) != 1 {
		t.Fatal("disabled media accepted")
	}
}

func TestLiveRevocationRejectsLateSessionCreation(t *testing.T) {
	m := newSessionManager(time.Minute)
	defer m.close()
	endpoint := ServiceEndpoint{"tcp", "127.0.0.1", 22}
	old := Config{Provide: []Provide{{ID: "gone", Service: endpoint}}, Consume: []Consume{{ID: "local", Expose: HostPort{"127.0.0.1", 12345}}}}
	m.reconfigure(old, Config{})
	if _, err := m.consumer(tunnel{id: "local", protocol: "tcp", expose: old.Consume[0].Expose}, "peer"); !errors.Is(err, context.Canceled) {
		t.Fatalf("late listener: %v", err)
	}
	key := providerSessionKey{mapping: "gone", fingerprint: "peer"}
	if _, err := m.providerTCP(context.Background(), key, false, endpoint); !errors.Is(err, context.Canceled) {
		t.Fatalf("late TCP dial: %v", err)
	}
	endpoint.Protocol = "udp"
	if _, err := m.providerUDP(key, endpoint); !errors.Is(err, context.Canceled) {
		t.Fatalf("late UDP group: %v", err)
	}
}

func TestLiveVoiceCancellationPreservesCongestedQUIC(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	conn := &congestedVoiceDatagrams{entered: make(chan struct{}), closed: make(chan struct{})}
	p := &muxPeer{ctx: ctx, voiceSend: make(chan voiceSendRequest, 8)}
	p.voiceEnabled.Store(true)
	writer := make(chan struct{})
	go func() { p.writeVoiceTo(conn); close(writer) }()
	voiceCtx, cancel := context.WithCancel(ctx)
	sent := make(chan error, 1)
	go func() { sent <- p.enqueueVoice(voiceCtx, []byte("voice")) }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("sender did not block")
	}
	cancel()
	select {
	case err := <-sent:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("voice cancellation blocked")
	}
	select {
	case <-conn.closed:
		t.Fatal("voice cancellation closed QUIC")
	default:
	}
	stop()
	select {
	case <-writer:
	case <-time.After(time.Second):
		t.Fatal("transport sender leaked")
	}
}

func TestLegacySessionTimeoutDoesNotRejoin(t *testing.T) {
	url, joins := recordingSignal(t)
	e := NewEngine(Options{Platform: &loopbackPlatform{opened: make(chan net.PacketConn, 128)}, RetryNetwork: true})
	defer e.Close()
	r := engineRequest()
	r.ServerURL = url
	r.Config.Provide = nil
	r.Config.Consume = nil
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	nextJoin(t, joins)
	waitSnapshot(t, e, func(s Snapshot) bool { return s.SignalState == "joined" })
	before := e.Snapshot().TransportGeneration
	r.Config.SessionTimeout = 83 * time.Second
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joins:
		t.Fatal("local retention setting rejoined IPv6 signaling")
	case <-time.After(30 * time.Millisecond):
	}
	if s := e.Snapshot(); s.TransportGeneration != before || s.EngineState != "running" {
		t.Fatal(s)
	}
	e.mu.Lock()
	manager := e.run.sessions
	e.mu.Unlock()
	manager.mu.Lock()
	timeout := manager.timeout
	manager.mu.Unlock()
	if timeout != 83*time.Second {
		t.Fatal(timeout)
	}
}
