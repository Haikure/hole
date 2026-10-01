package core

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func capacityFixture(t *testing.T) (int, <-chan struct{}, *Engine) {
	t.Helper()
	server, _ := actualWorkerFixture(t)
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	closed := make(chan struct{}, maxTCPSessions+1)
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn); closed <- struct{}{} }()
		}
	}()
	a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	t.Cleanup(func() { b.Close(); a.Close() })
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	port := unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "capacity", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "capacity", Expose: HostPort{"127.0.0.1", port}}}
	if err = a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err = b.Start(rb); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, b, func(s Snapshot) bool { return len(s.PeerTransports) == 1 && s.PeerTransports[0].ActiveChannels == 1 })
	return port, closed, a
}

func capacityDial(t *testing.T, port int) *net.TCPConn {
	t.Helper()
	conn, err := net.DialTCP("tcp4", nil, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write([]byte("ping"))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err = io.ReadFull(conn, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo: %q %v", got, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn
}

func TestICEMuxSupportsFullTCPSessionBudget(t *testing.T) {
	port, _, provider := capacityFixture(t)
	for i := 0; i < maxTCPSessions; i++ {
		capacityDial(t, port)
	}
	if got := provider.Snapshot().Mappings[0].TCPSessions; got != maxTCPSessions {
		t.Fatalf("only %d sessions admitted", got)
	}
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection beyond budget admitted")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("connection beyond budget hung")
	}
}

func TestClientResetRetiresProviderSession(t *testing.T) {
	port, closed, provider := capacityFixture(t)
	conn := capacityDial(t, port)
	_ = conn.SetLinger(0)
	_ = conn.Close()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("client RST left upstream open")
	}
	waitSnapshot(t, provider, func(s Snapshot) bool { return s.Mappings[0].TCPSessions == 0 })
}
