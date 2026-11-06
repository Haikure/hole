package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pion/stun/v4"
)

// Ordinary ICE sees no candidates; the supplementary sockets and all STUN
// endpoints stay on loopback. No test contacts a router or a public STUN server.
type natFixturePlatform struct{ DefaultPlatform }

func (natFixturePlatform) InterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	return []InterfaceSnapshot{}, nil
}

func TestNATSupplementaryQUICBusinessAndNetworkRecovery(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	urls := natFixtureSTUNURLs(t)
	runNATSupplementary(t, server, urls)
}

func natFixtureSTUNURLs(t *testing.T) []string {
	t.Helper()
	urls := []string{}
	for range 2 {
		stunServer, e := net.ListenPacket("udp4", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = stunServer.Close() })
		urls = append(urls, "stun:"+stunServer.LocalAddr().String())
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, e := stunServer.ReadFrom(buf)
				if e != nil {
					return
				}
				m := new(stun.Message)
				m.Raw = append(m.Raw, buf[:n]...)
				if m.Decode() != nil {
					continue
				}
				a := from.(*net.UDPAddr)
				reply := stun.MustBuild(stun.NewTransactionIDSetter(m.TransactionID), stun.BindingSuccess, &stun.XORMappedAddress{IP: a.IP, Port: a.Port})
				_, _ = stunServer.WriteTo(reply.Raw, from)
			}
		}()
	}
	return urls
}

func TestNATStatusWithOrdinaryICEAndPunchingDisabled(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	urls := natFixtureSTUNURLs(t)
	a, b := NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	for _, r := range []*Request{&ra, &rb} {
		r.Config.ICE.UDPPunching = "off"
		r.Config.ICE.NATSTUNURLs = urls
	}
	ra.Config.Provide = []Provide{{ID: "status", Service: ServiceEndpoint{"tcp", "127.0.0.1", unusedTCPPort(t)}}}
	rb.Config.Consume = []Consume{{ID: "status", Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	reported := func(s Snapshot) bool {
		if len(s.PeerTransports) != 1 {
			return false
		}
		p := s.PeerTransports[0]
		return p.State == "active" && p.PathType == "direct" && p.DirectStrategy == "" && p.PeerNAT.ProbedAt != "" && p.PeerNAT.Samples == 6
	}
	waitSnapshot(t, a, reported)
	waitSnapshot(t, b, reported)
	hibernate()
	old := a.Snapshot().PeerTransports[0].Generation
	if err := a.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, func(s Snapshot) bool { return reported(s) && s.PeerTransports[0].Generation > old })
	waitSnapshot(t, b, reported)
}

func runNATSupplementary(t *testing.T, server string, urls []string) {
	t.Helper()
	tcp, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer tcp.Close()
	go func() {
		for {
			c, e := tcp.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	udp, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, e := udp.ReadFrom(b)
			if e != nil {
				return
			}
			_, _ = udp.WriteTo(b[:n], a)
		}
	}()
	a, b := NewEngine(Options{Platform: natFixturePlatform{}}), NewEngine(Options{Platform: natFixturePlatform{}})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	for _, r := range []*Request{&ra, &rb} {
		r.Config.ICE.NATSTUNURLs = urls
		r.Config.ICE.PortMapping = "off"
		r.Config.ICE.DirectProbeTimeout = ConfigDuration(200 * time.Millisecond)
	}
	tp, up := unusedTCPPort(t), unusedUDPPort(t)
	ra.Config.Provide = []Provide{{ID: "tcp", Service: ServiceEndpoint{"tcp", "127.0.0.1", tcp.Addr().(*net.TCPAddr).Port}}, {ID: "udp", Service: ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "tcp", Expose: HostPort{"127.0.0.1", tp}}, {ID: "udp", Expose: HostPort{"127.0.0.1", up}}}
	if e = a.Start(ra); e != nil {
		t.Fatal(e)
	}
	if e = b.Start(rb); e != nil {
		t.Fatal(e)
	}
	ready := func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 2 && s.PeerTransports[0].DirectStrategy == "bidirectional"
	}
	waitSnapshot(t, a, ready)
	waitSnapshot(t, b, ready)
	var established []net.Conn
	for _, v := range []struct {
		network string
		port    int
	}{{"tcp", tp}, {"udp", up}} {
		conn, e := net.Dial(v.network, fmt.Sprintf("127.0.0.1:%d", v.port))
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		established = append(established, conn)
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = conn.Write([]byte("hello"))
		buf := make([]byte, 5)
		if _, e = io.ReadFull(conn, buf); e != nil || string(buf) != "hello" {
			t.Fatalf("%s %q %v", v.network, buf, e)
		}
	}
	old := a.Snapshot().PeerTransports[0].Generation
	if e = a.NetworkChanged(); e != nil {
		t.Fatal(e)
	}
	waitSnapshot(t, a, func(s Snapshot) bool { return ready(s) && s.PeerTransports[0].Generation > old })
	for _, conn := range established {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = conn.Write([]byte("again"))
		buf := make([]byte, 5)
		if _, e = io.ReadFull(conn, buf); e != nil || string(buf) != "again" {
			t.Fatalf("session recovery %q %v", buf, e)
		}
	}
}
