package core

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A real loopback transport: only platform addressing differs from Android.
// The actual WebSocket, TLS/QUIC, TCP replay and UDP fragmentation code runs.
type forwardingPlatform struct {
	DefaultPlatform
	port atomic.Int32
}

func (p *forwardingPlatform) Candidates(context.Context, Config, int) ([]Candidate, error) {
	return []Candidate{{IP: "::1", Port: int(p.port.Load())}}, nil
}
func (p *forwardingPlatform) ListenPacket(ctx context.Context, _, _ string) (net.PacketConn, error) {
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp6", "[::1]:0")
	if err == nil {
		p.port.Store(int32(conn.LocalAddr().(*net.UDPAddr).Port))
	}
	return conn, err
}

type signalTestPeer struct {
	conn *websocket.Conn
	join SignalMessage
}
type signalTestHub struct {
	mu    sync.Mutex
	peers map[string]*signalTestPeer
}

func (h *signalTestHub) readyLocked() {
	for _, provider := range h.peers {
		for _, provided := range provider.join.Provide {
			for _, consumer := range h.peers {
				if consumer == provider {
					continue
				}
				for _, consumed := range consumer.join.Consume {
					if consumed.ID != provided.ID || provider.join.Room != consumer.join.Room || provider.join.Token != consumer.join.Token {
						continue
					}
					service := provided.Service
					_ = provider.conn.WriteJSON(SignalMessage{Type: "mapping_ready", MappingID: provided.ID, Role: "provider", PeerDevice: consumer.join.DeviceID, PeerFingerprint: consumer.join.CertFingerprint, PeerCandidates: consumer.join.Candidates, Service: &service})
					_ = consumer.conn.WriteJSON(SignalMessage{Type: "mapping_ready", MappingID: provided.ID, Role: "consumer", PeerDevice: provider.join.DeviceID, PeerFingerprint: provider.join.CertFingerprint, PeerCandidates: provider.join.Candidates, Service: &service})
				}
			}
		}
	}
}
func (h *signalTestHub) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	var join SignalMessage
	if conn.ReadJSON(&join) != nil {
		return
	}
	peer := &signalTestPeer{conn: conn, join: join}
	h.mu.Lock()
	if old := h.peers[join.DeviceID]; old != nil {
		_ = old.conn.Close()
	}
	h.peers[join.DeviceID] = peer
	_ = conn.WriteJSON(SignalMessage{Type: "joined"})
	h.readyLocked()
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.peers[join.DeviceID] != peer {
			return
		}
		delete(h.peers, join.DeviceID)
		for _, remaining := range h.peers {
			for _, provided := range join.Provide {
				_ = remaining.conn.WriteJSON(SignalMessage{Type: "mapping_closed", MappingID: provided.ID, PeerDevice: join.DeviceID})
			}
			for _, consumed := range join.Consume {
				_ = remaining.conn.WriteJSON(SignalMessage{Type: "mapping_closed", MappingID: consumed.ID, PeerDevice: join.DeviceID})
			}
		}
	}()
	for {
		var message SignalMessage
		if conn.ReadJSON(&message) != nil {
			return
		}
	}
}
func unusedTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
func unusedUDPPort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	return port
}

func TestEngineBidirectionalTCPUDPResumeAcrossReconfigureAndNetwork(t *testing.T) {
	hub := &signalTestHub{peers: make(map[string]*signalTestPeer)}
	server := httptest.NewServer(http.HandlerFunc(hub.serve))
	defer server.Close()
	tcpEcho, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpEcho.Close()
	var accepts atomic.Int32
	go func() {
		for {
			conn, err := tcpEcho.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	udpEcho, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := udpEcho.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = udpEcho.WriteTo(buf[:n], addr)
		}
	}()
	tcpExpose, udpExpose := unusedTCPPort(t), unusedUDPPort(t)
	a, b := NewEngine(Options{Platform: &forwardingPlatform{}, RetryNetwork: true}), NewEngine(Options{Platform: &forwardingPlatform{}, RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	defer func() {
		if !t.Failed() {
			return
		}
		for name, engine := range map[string]*Engine{"a": a, "b": b} {
			t.Logf("%s snapshot: %+v", name, engine.Snapshot())
			for {
				select {
				case event := <-engine.Events():
					t.Logf("%s: %+v", name, event)
				default:
					goto drained
				}
			}
		drained:
		}
	}()
	ra := engineRequest()
	ra.ServerURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ra.Config.DeviceName = "device-a"
	ra.Config.Provide = []Provide{{ID: "tcp", Service: ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: tcpEcho.Addr().(*net.TCPAddr).Port}}}
	ra.Config.Consume = []Consume{{ID: "udp", Expose: HostPort{Addr: "127.0.0.1", Port: udpExpose}}}
	rb := ra
	rb.Config = ra.Config.normalized()
	rb.Config.DeviceName = "device-b"
	rb.Config.Provide = []Provide{{ID: "udp", Service: ServiceEndpoint{Protocol: "udp", Addr: "127.0.0.1", Port: udpEcho.LocalAddr().(*net.UDPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "tcp", Expose: HostPort{Addr: "127.0.0.1", Port: tcpExpose}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	active := func(s Snapshot) bool {
		for _, m := range s.Mappings {
			if m.Role == "consume" && m.State == "active" {
				return true
			}
		}
		return false
	}
	waitSnapshot(t, a, active)
	waitSnapshot(t, b, active)
	tcp, err := net.Dial("tcp", endpointString("127.0.0.1", tcpExpose))
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp1, err := net.Dial("udp", endpointString("127.0.0.1", udpExpose))
	if err != nil {
		t.Fatal(err)
	}
	defer udp1.Close()
	udp2, err := net.Dial("udp", endpointString("127.0.0.1", udpExpose))
	if err != nil {
		t.Fatal(err)
	}
	defer udp2.Close()
	exchangeTCP := func(data []byte) {
		t.Helper()
		_ = tcp.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := tcp.Write(data); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(data))
		if _, err := io.ReadFull(tcp, got); err != nil {
			t.Fatal("TCP resume failed", err)
		}
		if !bytes.Equal(data, got) {
			t.Fatal("TCP bytes duplicated/reordered")
		}
	}
	exchangeUDP := func(conn net.Conn, data []byte) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			_ = conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
			_, _ = conn.Write(data)
			got := make([]byte, 65535)
			n, err := conn.Read(got)
			if err == nil && bytes.Equal(data, got[:n]) {
				return
			}
		}
		t.Fatal("UDP mapping failed to resume")
	}
	exchangeTCP(bytes.Repeat([]byte("before-"), 8000))
	exchangeUDP(udp1, bytes.Repeat([]byte("first-source-"), 1000))
	exchangeUDP(udp2, []byte("second source"))
	a.mu.Lock()
	sessionsA, runA := a.run.sessions, a.run
	a.mu.Unlock()
	ra.Config.Provide = append(ra.Config.Provide, Provide{ID: "unused", Service: ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: 1}})
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, func(s Snapshot) bool { return s.SignalState == "joined" && len(s.Mappings) == 3 })
	exchangeTCP(bytes.Repeat([]byte("after-config-"), 8000))
	if err := a.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	if err := b.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, func(s Snapshot) bool { return s.SignalState == "joined" && s.NetworkChanges == 1 })
	waitSnapshot(t, b, func(s Snapshot) bool { return s.SignalState == "joined" && s.NetworkChanges == 1 })
	exchangeTCP(bytes.Repeat([]byte("after-network-"), 8000))
	exchangeUDP(udp1, []byte("same first source after network"))
	exchangeUDP(udp2, bytes.Repeat([]byte("second-"), 1000))
	if accepts.Load() != 1 {
		t.Fatalf("existing application TCP socket was replaced: accepted %d", accepts.Load())
	}
	a.mu.Lock()
	same := sessionsA == a.run.sessions && runA == a.run
	a.mu.Unlock()
	if !same {
		t.Fatal("network operation replaced engine/session manager")
	}
	waitSnapshot(t, a, func(s Snapshot) bool {
		for _, m := range s.Mappings {
			if m.ID == "udp" {
				return m.UDPSessions == 2
			}
		}
		return false
	})
	// Deleting a mapping ends its application socket, unlike a path migration.
	rb.Config.Consume = nil
	if err := b.ApplyConfig(rb); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "joined" && len(s.Mappings) == 1 &&
			s.Mappings[0].ID == "udp" && s.Mappings[0].Role == "provide"
	})
	_ = tcp.SetReadDeadline(time.Now().Add(3 * time.Second))
	var one [1]byte
	if _, err := tcp.Read(one[:]); err == nil {
		t.Fatal("deleted mapping kept TCP socket alive")
	}
}
