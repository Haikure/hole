package core

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"testing"
	"time"
)

func TestHoleThreeDevices(t *testing.T) {
	for _, family := range []string{"udp4", "udp6"} {
		t.Run(family, func(t *testing.T) {
			server, _ := actualWorkerFixture(t, ICEServer{})
			relay := NewEngine(Options{AllowRelayServe: true, RetryNetwork: true})
			a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
			defer relay.Close()
			defer a.Close()
			defer b.Close()
			rr := iceFixtureRequest(server, "relay-node")
			rr.Config.Transport.FixedEndpoint = fixedEndpoint(t, family)
			rr.Config.Relay.Serve.Enabled = true
			rr.Config.TURN.Mode = "off"
			if err := relay.Start(rr); err != nil {
				t.Fatal(err)
			}
			waitSnapshotWithin(t, relay, 5*time.Second, func(s Snapshot) bool { return s.SignalState == "joined" })
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
			udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			go func() {
				buf := make([]byte, 65535)
				for {
					n, addr, e := udp.ReadFrom(buf)
					if e != nil {
						return
					}
					_, _ = udp.WriteTo(buf[:n], addr)
				}
			}()
			ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
			for _, r := range []*Request{&ra, &rb} {
				r.Config.ICE.RelayOnly = true
				r.Config.TURN.Mode = "off"
				r.Config.Voice.Enabled = true
			}
			// Only one endpoint proposes Hole. The peer participates in its shared step.
			ra.Config.Relay.Device = "relay-node"
			ra.Config.Provide = []Provide{{ID: "tcp", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}, {ID: "udp", Service: ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}}}
			// The relay simultaneously provides and consumes ordinary fixed-port
			// services on the same public socket used by its forwarding backend.
			relayExpose, alphaExpose, reverseExpose := unusedTCPPort(t), unusedTCPPort(t), unusedTCPPort(t)
			rr.Config.Provide = []Provide{{ID: "relay-tcp", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
			rr.Config.Consume = []Consume{{ID: "tcp", Expose: HostPort{"127.0.0.1", relayExpose}}}
			ra.Config.Consume = []Consume{{ID: "relay-tcp", Expose: HostPort{"127.0.0.1", alphaExpose}}, {ID: "reverse", Expose: HostPort{"127.0.0.1", reverseExpose}}}
			rb.Config.Provide = []Provide{{ID: "reverse", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
			// A fixed relation cannot satisfy relay-only; keep the ordinary alpha
			// endpoint unrestricted while beta requires their shared Hole path.
			ra.Config.ICE.RelayOnly = false
			if err = relay.ApplyConfig(rr); err != nil {
				t.Fatal(err)
			}
			tcpPort, udpPort := unusedTCPPort(t), unusedTCPPort(t)
			rb.Config.Consume = []Consume{{ID: "tcp", Expose: HostPort{"127.0.0.1", tcpPort}}, {ID: "udp", Expose: HostPort{"127.0.0.1", udpPort}}}
			// An endpoint with no local relay proposal must be allowed in relay-only
			// mode when TURN is off: it can use the other endpoint's Hole proposal.
			rb.Config.ICE.RelayOnly = true
			if err = a.Start(ra); err != nil {
				t.Fatal(err)
			}
			if err = b.Start(rb); err != nil {
				t.Fatal(err)
			}
			active := func(s Snapshot) bool {
				for _, p := range s.PeerTransports {
					if p.PathType == "hole" && p.State == "active" && p.ActiveChannels == 3 {
						return true
					}
				}
				return false
			}
			left := waitSnapshotWithin(t, a, 15*time.Second, active)
			right := waitSnapshotWithin(t, b, 15*time.Second, active)
			if !active(left) || right.PeerTransports[0].RelayDevice != "relay-node" {
				t.Fatalf("wrong path: %+v %+v", left, right)
			}
			c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", tcpPort), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			payload := []byte("three device relay")
			_, _ = c.Write(payload)
			got := make([]byte, len(payload))
			if _, err = io.ReadFull(c, got); err != nil || string(got) != string(payload) {
				t.Fatal("TCP", err, string(got))
			}
			u, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", udpPort))
			if err != nil {
				t.Fatal(err)
			}
			defer u.Close()
			_ = u.SetDeadline(time.Now().Add(3 * time.Second))
			_, _ = u.Write(payload)
			got = make([]byte, 100)
			n, err := u.Read(got)
			if err != nil || string(got[:n]) != string(payload) {
				t.Fatal("UDP", err)
			}
			for _, port := range []int{relayExpose, alphaExpose, reverseExpose} {
				conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
				if err != nil {
					t.Fatal("relay's ordinary service", err)
				}
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = conn.Write(payload)
				got := make([]byte, len(payload))
				_, err = io.ReadFull(conn, got)
				conn.Close()
				if err != nil || string(got) != string(payload) {
					t.Fatal("shared socket fixed service", err)
				}
			}
			// Send actual PCM through Opus and the shared Hole QUIC DATAGRAM path.
			pcm := make([]byte, 1920)
			for i := 0; i < 960; i++ {
				binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(8000*math.Sin(2*math.Pi*440*float64(i)/48000))))
			}
			ticker := time.NewTicker(20 * time.Millisecond)
			for i := 0; i < 40; i++ {
				<-ticker.C
				for _, engine := range []*Engine{a, b} {
					if err := engine.PushVoicePCM(int64(i), int64(i*960), pcm); err != nil {
						t.Fatal(err)
					}
					_ = engine.PullVoicePCM(4)
				}
			}
			ticker.Stop()
			for _, engine := range []*Engine{a, b} {
				voice := engine.Snapshot().Voice
				if len(voice.Peers) != 1 || voice.Peers[0].ReceivedFrames < 10 || voice.Peers[0].DecodedFrames < 10 {
					t.Fatalf("Hole voice did not transmit/decode: %+v", voice)
				}
			}
			for _, engine := range []*Engine{a, b} {
				snapshot := waitSnapshotWithin(t, engine, time.Second, func(s Snapshot) bool {
					for _, peer := range s.PeerTransports {
						if peer.PathType == "hole" && peer.RTTMS > 0 {
							return true
						}
					}
					return false
				})
				measured := false
				for _, peer := range snapshot.PeerTransports {
					measured = measured || (peer.PathType == "hole" && peer.RTTMS > 0)
				}
				if !measured {
					t.Fatalf("Hole QUIC RTT was not measured: %+v", snapshot.PeerTransports)
				}
			}
			// Preserve the existing application socket across endpoint and relay
			// network generations, including both forwarding and fixed traffic.
			for _, changed := range []*Engine{b, relay} {
				before := b.Snapshot().PeerTransports[0].Generation
				if err := changed.NetworkChanged(); err != nil {
					t.Fatal(err)
				}
				waitSnapshotWithin(t, b, 15*time.Second, func(s Snapshot) bool { return active(s) && s.PeerTransports[0].Generation > before })
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = c.Write(payload)
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(c, got); err != nil || string(got) != string(payload) {
					t.Fatal("TCP session lost across Hole reconnect", err)
				}
			}
			rs := relay.Snapshot()
			if rs.Relay == nil || rs.Relay.ActivePairs != 1 || rs.Relay.ForwardedBytes == 0 {
				t.Fatalf("missing relay stats: %+v", rs.Relay)
			}
		})
	}
}

func TestHoleDatagramPath(t *testing.T) {
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relay := newHoleForwarder(context.Background(), socket, RelayServeConfig{MaxPairs: 1})
	defer relay.Close()
	grant := &holeGrant{Route: "01234567890123456789012345678901", Keys: [2]string{"0123456789012345678901234567890101234567890123456789012345678901", "1123456789012345678901234567890101234567890123456789012345678901"}, Endpoint: socket.LocalAddr().String(), Generation: 1}
	if err = relay.install(iceSignalMessage{Hole: grant, TransportGeneration: 1, LeaseUntil: time.Now().Add(time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := openHolePath(ctx, DefaultPlatform{}, grant, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := openHolePath(ctx, DefaultPlatform{}, grant, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, err = a.WriteTo([]byte("payload"), b.PeerAddr())
	if err != nil {
		t.Fatal(err)
	}
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 100)
	n, _, err := b.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "payload" {
		t.Fatal(err, string(buf[:n]))
	}
}
