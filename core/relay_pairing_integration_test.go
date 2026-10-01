package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Keep the port-80 selection policy under test without binding a privileged or
// occupied port on the host. Only the fixture TURN address is redirected.
type turnPortFixture struct {
	DefaultPlatform
	target string
}

func (p turnPortFixture) DialSignal(ctx context.Context, network, address string) (net.Conn, error) {
	if address == "127.0.0.1:80" {
		address = p.target
	}
	return p.DefaultPlatform.DialSignal(ctx, network, address)
}

func TestMixedManualAndWorkerRelayPairing(t *testing.T) {
	for _, staggered := range []bool{false, true} {
		t.Run(fmt.Sprintf("staggered=%v", staggered), func(t *testing.T) {
			udpURL, stopUDP := startTURNRelay(t, "udp")
			defer stopUDP()
			tcpURL, stopTCP := startTURNRelay(t, "tcp")
			defer stopTCP()
			leftURL, rightURL := "turn:127.0.0.1:80", udpURL
			var platform Platform = turnPortFixture{target: strings.TrimSuffix(strings.TrimPrefix(tcpURL, "turn:"), "?transport=tcp")}
			leftProtocol, rightProtocol := "tcp", "udp"
			if staggered {
				leftURL, rightURL = udpURL, tcpURL
				platform = DefaultPlatform{}
				leftProtocol, rightProtocol = "udp", "tcp"
			}
			server, _ := actualWorkerFixture(t, ICEServer{URLs: []string{rightURL}, Username: "user", Credential: "secret"})
			echo, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer echo.Close()
			go func() {
				for {
					c, e := echo.Accept()
					if e != nil {
						return
					}
					go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
				}
			}()
			a, b := NewEngine(Options{Platform: platform, RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
			defer a.Close()
			defer b.Close()
			ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
			ra.Config.ICE.RelayOnly, rb.Config.ICE.RelayOnly = true, true
			ra.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{leftURL}, Username: "user", Credential: "secret", Order: []string{"tcp", "udp"}}
			rb.Config.TURN = TURNConfig{Mode: "worker"}
			port := unusedTCPPort(t)
			ra.Config.Provide = []Provide{{ID: "mixed", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
			rb.Config.Consume = []Consume{{ID: "mixed", Expose: HostPort{"127.0.0.1", port}}}
			if err = a.Start(ra); err != nil {
				t.Fatal(err)
			}
			if err = b.Start(rb); err != nil {
				t.Fatal(err)
			}
			active := func(s Snapshot) bool {
				return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 1
			}
			left := waitSnapshotWithin(t, a, 30*time.Second, active).PeerTransports[0]
			right := waitSnapshotWithin(t, b, 30*time.Second, active).PeerTransports[0]
			if left.LocalRelayProtocol != leftProtocol || right.LocalRelayProtocol != rightProtocol || left.RelaySide != "both" || right.RelaySide != "both" {
				t.Fatalf("incorrect selected relays: %+v / %+v", left, right)
			}
			if staggered && left.RelayRound <= 1 {
				t.Fatal("did not exercise cross-round pairing")
			}
			c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			_, _ = c.Write([]byte("mixed-turn"))
			got := make([]byte, 10)
			if _, err = io.ReadFull(c, got); err != nil || string(got) != "mixed-turn" {
				t.Fatal("relay traffic failed", err, string(got))
			}
		})
	}
}
