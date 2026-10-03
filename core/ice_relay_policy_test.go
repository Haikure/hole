package core

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestRelayOnlyHotChangeAndNetworkRecovery(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	relayURL, stop := startTURNRelay(t, "udp")
	defer stop()
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
	a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	rb.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{relayURL}, Username: "user", Credential: "secret"}
	port := unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "policy", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "policy", Expose: HostPort{"127.0.0.1", port}}}
	if err = a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err = b.Start(rb); err != nil {
		t.Fatal(err)
	}
	active := func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 1
	}
	first := waitSnapshotWithin(t, b, 10*time.Second, active).PeerTransports[0]
	if first.PathType != "direct" {
		t.Fatal(first)
	}
	waitSnapshotWithin(t, a, 10*time.Second, active)
	oldLive := func(e *Engine) *liveICETransport {
		e.mu.Lock()
		c := e.run.ice
		e.mu.Unlock()
		p := c.peerList()[0]
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.active
	}
	oldA, oldB := oldLive(a), oldLive(b)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	transfer := func(payload string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != payload {
			t.Fatal(err, string(buf))
		}
	}
	transfer("direct")
	rb.Config.ICE.RelayOnly = true
	if err = b.ApplyConfig(rb); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldB.session.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("local old direct path survived configuration change")
	}
	relayActive := func(s Snapshot) bool {
		return active(s) && s.PeerTransports[0].Generation > first.Generation && s.PeerTransports[0].PathType == "relay"
	}
	left := waitSnapshotWithin(t, a, 15*time.Second, relayActive).PeerTransports[0]
	right := waitSnapshotWithin(t, b, 15*time.Second, relayActive).PeerTransports[0]
	if left.RelaySide != "remote" || right.RelaySide != "local" {
		t.Fatal(left, right)
	}
	select {
	case <-oldA.session.Context().Done():
	default:
		t.Fatal("remote old direct path survived policy notification")
	}
	transfer("relay-after-change")
	if err = b.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	recovered := func(s Snapshot) bool { return relayActive(s) && s.PeerTransports[0].Generation > right.Generation }
	waitSnapshotWithin(t, a, 20*time.Second, recovered)
	waitSnapshotWithin(t, b, 20*time.Second, recovered)
	transfer("relay-after-network-change")
	for _, e := range []*Engine{a, b} {
		for len(e.Events()) > 0 {
			event := <-e.Events()
			if event.Kind == "transport" && event.State == "checking" && event.Phase == "direct" && event.TransportGeneration > first.Generation {
				t.Fatal("returned to direct after relay-only enabled", event)
			}
		}
	}
}
