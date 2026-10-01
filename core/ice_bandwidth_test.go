package core

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// Runs two complete Engines over actual loopback ICE / QUIC, with application
// TCP sockets at both ends. A persistent connection excludes setup/rejoin time.
// Both endpoints share this host's CPU; this is not an Internet or device test.
func BenchmarkICETCP(b *testing.B) {
	const transfer = 32 * 1024 * 1024
	server, _ := actualWorkerFixture(b)
	service, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { service.Close() })
	// Allocate an expose port without making assumptions about the host.
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	reserved.Close()
	provider, consumer := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	b.Cleanup(func() { consumer.Close(); provider.Close() })
	p, c := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	p.Config.Provide = []Provide{{ID: "bandwidth", Service: ServiceEndpoint{"tcp", "127.0.0.1", service.Addr().(*net.TCPAddr).Port}}}
	c.Config.Consume = []Consume{{ID: "bandwidth", Expose: HostPort{"127.0.0.1", port}}}
	if err = provider.Start(p); err != nil {
		b.Fatal(err)
	}
	if err = consumer.Start(c); err != nil {
		b.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := consumer.Snapshot()
		if len(s.PeerTransports) == 1 && s.PeerTransports[0].ActiveChannels == 1 {
			break
		}
		if time.Now().After(deadline) {
			b.Fatal("ICE transport did not become ready", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
	application, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { application.Close() })
	_ = service.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	upstream, err := service.Accept()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { upstream.Close() })
	_ = application.SetDeadline(time.Now().Add(2 * time.Minute))
	_ = upstream.SetDeadline(time.Now().Add(2 * time.Minute))
	received := make(chan error, 1)
	iterations := b.N + 1
	go func() {
		for i := 0; i < iterations; i++ {
			_, err := io.CopyN(io.Discard, upstream, transfer)
			received <- err
			if err != nil {
				return
			}
		}
	}()
	data := make([]byte, 64*1024)
	send := func() {
		for sent := 0; sent < transfer; sent += len(data) {
			if err := writeFull(application, data); err != nil {
				b.Fatal(err)
			}
		}
		if err := <-received; err != nil {
			b.Fatal(err)
		}
	}
	// Warm congestion control, PMTU discovery and reusable buffers first.
	send()
	b.SetBytes(transfer)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		send()
	}
	b.StopTimer()
}
