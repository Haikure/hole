package core

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// One local three-device scenario covers the real Worker, both QUIC layers,
// fragmented datagrams and fallback without depending on Internet NAT behavior.
func TestPeerRelayThreeDevicesAndFallback(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	turnURL, stopTURN := startTURNRelay(t, "udp")
	defer stopTURN()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = udp.WriteTo(buf[:n], addr)
		}
	}()
	a, b, relay := NewEngine(Options{}), NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	defer relay.Close()
	ra, rb, rr := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta"), iceFixtureRequest(server, "relay")
	for _, r := range []*Request{&ra, &rb, &rr} {
		// Voice relationships provide the two already-established relay legs.
		r.Config.Voice.Enabled = true
		r.Config.ICE.PortMapping = "off"
		r.Config.ICE.NATSTUNURLs = []string{}
		r.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{turnURL}, Username: "user", Credential: "secret", Order: []string{"udp"}}
	}
	tcpPort, udpPort := unusedTCPPort(t), unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "tcp", Service: ServiceEndpoint{"tcp", "127.0.0.1", tcp.Addr().(*net.TCPAddr).Port}}, {ID: "udp", Service: ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "tcp", Expose: HostPort{"127.0.0.1", tcpPort}}, {ID: "udp", Expose: HostPort{"127.0.0.1", udpPort}}}
	for _, item := range []struct {
		e *Engine
		r Request
	}{{a, ra}, {b, rb}, {relay, rr}} {
		if err := item.e.Start(item.r); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []*Engine{a, b, relay} {
		waitSnapshotWithin(t, e, 8*time.Second, func(s Snapshot) bool {
			if len(s.PeerTransports) != 2 {
				return false
			}
			for _, p := range s.PeerTransports {
				if p.State != "active" || p.PathType != "direct" {
					return false
				}
			}
			return true
		})
	}
	a.mu.Lock()
	coordinator := a.run.ice
	a.mu.Unlock()
	var pair *icePeer
	for _, p := range coordinator.peerList() {
		if p.peerID() == "beta" {
			pair = p
		}
	}
	if pair == nil {
		t.Fatal("missing pair")
	}
	// Force only this pair past its direct budget; both relay legs stay real.
	pair.restartRound(1, "", "direct_exhausted")
	peerActive := func(s Snapshot) bool {
		for _, p := range s.PeerTransports {
			if p.PeerID == "alpha" || p.PeerID == "beta" {
				return p.State == "active" && p.PathType == "peer_relay" && p.RelayDevice == "relay" && p.ActiveChannels == 2
			}
		}
		return false
	}
	for _, e := range []*Engine{a, b} {
		waitSnapshotWithin(t, e, 12*time.Second, peerActive)
	}
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", tcpPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	checkTCP := func(text string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		if _, err := conn.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(text))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != text {
			t.Fatalf("TCP: %q %v", got, err)
		}
	}
	checkTCP("peer relay TCP")
	u, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", udpPort))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	_ = u.SetDeadline(time.Now().Add(8 * time.Second))
	payload := bytes.Repeat([]byte("fragmented"), 300)
	if _, err = u.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 65535)
	if n, err := u.Read(got); err != nil || !bytes.Equal(got[:n], payload) {
		t.Fatalf("UDP: %v", err)
	}
	hibernate()
	// Renew after hibernation to verify the persisted route can be replayed.
	_ = coordinator.send(signalMessage("transport_sync"))
	checkTCP("after Worker hibernation")
	if err := relay.Stop(); err != nil {
		t.Fatal(err)
	}
	// A failed outer leg must enter the held ordinary relay round, not skip it.
	waitSnapshotWithin(t, a, 12*time.Second, func(s Snapshot) bool {
		for _, p := range s.PeerTransports {
			if p.PeerID == "beta" {
				return p.State == "active" && p.Phase == "relay_udp" && p.PathType != "peer_relay"
			}
		}
		return false
	})
	checkTCP("same TCP socket after fallback")
}
