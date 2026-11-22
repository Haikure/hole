package core

import (
	"bytes"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

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
	server, _ := actualWorkerFixture(t)
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
	a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
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
	ra.ServerURL = server
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
	waitSnapshot(t, a, func(s Snapshot) bool {
		return (s.SignalState == "joined" || s.SignalState == "delegated") && len(s.Mappings) == 3
	})
	exchangeTCP(bytes.Repeat([]byte("after-config-"), 8000))
	if err := a.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	if err := b.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, func(s Snapshot) bool {
		return (s.SignalState == "joined" || s.SignalState == "delegated") && s.NetworkChanges == 1
	})
	waitSnapshot(t, b, func(s Snapshot) bool {
		return (s.SignalState == "joined" || s.SignalState == "delegated") && s.NetworkChanges == 1
	})
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
		return (s.SignalState == "joined" || s.SignalState == "delegated") && len(s.Mappings) == 1 &&
			s.Mappings[0].ID == "udp" && s.Mappings[0].Role == "provide"
	})
	_ = tcp.SetReadDeadline(time.Now().Add(3 * time.Second))
	var one [1]byte
	if _, err := tcp.Read(one[:]); err == nil {
		t.Fatal("deleted mapping kept TCP socket alive")
	}
}
