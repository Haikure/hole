package core

import (
	"net"
	"testing"

	"github.com/pion/stun/v4"
)

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

func TestNATStatusWithOrdinaryICEAndNetworkRecovery(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	urls := natFixtureSTUNURLs(t)
	a, b := NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	for _, r := range []*Request{&ra, &rb} {
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
		return p.State == "active" && p.PathType == "direct" && p.PeerNAT.ProbedAt != "" && p.PeerNAT.Samples == 6
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
