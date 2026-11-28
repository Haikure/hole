package core

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Real Worker, UDP, QUIC and an application socket exercise both finish orders
// and TURN fallback. The Hole endpoint is unreachable in the direct-first case.
func TestParallelStartPreservesTCPAndReleasesRelay(t *testing.T) {
	for _, scenario := range []string{"hole_first", "direct_first", "turn_fallback", "turn_direct"} {
		t.Run(scenario, func(t *testing.T) {
			turnDirect := scenario == "turn_direct"
			directFirst, turnFallback := scenario == "direct_first" || turnDirect, scenario == "turn_fallback" || turnDirect
			if turnDirect {
				t.Setenv("HOLE_FIXTURE_DROP_PARALLEL_DIRECT_ICE", "1")
			}
			t.Setenv("HOLE_FIXTURE_COMMIT_DELAY", "300")
			t.Setenv("HOLE_FIXTURE_DROP_UPGRADE_ESTABLISHED", "1")
			server, hibernate := actualWorkerFixture(t)
			var turnURL, turnAddress string
			if turnFallback {
				var stopTURN func()
				turnURL, stopTURN = startTURNRelay(t, "udp", upgradeTURNNetwork{})
				defer stopTURN()
				_, turnAddress, _ = strings.Cut(turnURL, ":")
				turnAddress, _, _ = strings.Cut(turnAddress, "?")
			}
			echo, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer echo.Close()
			var accepted atomic.Int32
			go func() {
				for {
					conn, err := echo.Accept()
					if err != nil {
						return
					}
					accepted.Add(1)
					go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
				}
			}()
			holeAddress := fmt.Sprintf("127.0.0.1:%d", unusedUDPPort(t))
			available := &atomic.Bool{}
			platform := func() *upgradeTestPlatform {
				p := &upgradeTestPlatform{relayLegPlatform: relayLegPlatform{allowed: map[string]bool{}, allowAll: available}, address: "127.0.0.1"}
				if directFirst {
					p.address = "127.0.0.2"
				} else {
					p.allowed[holeAddress] = true
				}
				if turnFallback {
					p.allowed[turnAddress] = true
				}
				return p
			}
			a := NewEngine(Options{Platform: platform()})
			b := NewEngine(Options{Platform: platform()})
			hole := NewEngine(Options{AllowRelayServe: true})
			defer a.Close()
			defer b.Close()
			defer hole.Close()
			rh := iceFixtureRequest(server, "hole-node")
			rh.Config.Transport.FixedEndpoint = &FixedEndpointConfig{Listen: holeAddress, Advertise: holeAddress}
			rh.Config.Relay.Serve.Enabled = true
			if !turnFallback {
				if err := hole.Start(rh); err != nil {
					t.Fatal(err)
				}
				waitSnapshotWithin(t, hole, 3*time.Second, func(s Snapshot) bool { return s.SignalState == "joined" })
			}
			ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
			for _, request := range []*Request{&ra, &rb} {
				request.Config.Relay.Device = "hole-node"
				request.Config.Relay.Order = []string{"hole"}
				request.Config.ICE.PortMapping = "off"
				request.Config.ICE.DirectProbeTimeout = ConfigDuration(4 * time.Second)
				request.Config.Voice.Enabled = true
				if turnFallback {
					request.Config.Relay.Order = []string{"hole", "udp"}
					request.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{turnURL}, Username: "user", Credential: "secret", Order: []string{"udp"}}
				}
			}
			port := unusedTCPPort(t)
			ra.Config.Provide = []Provide{{ID: "echo", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
			rb.Config.Consume = []Consume{{ID: "echo", Expose: HostPort{"127.0.0.1", port}}}
			if err := a.Start(ra); err != nil {
				t.Fatal(err)
			}
			if err := b.Start(rb); err != nil {
				t.Fatal(err)
			}
			waitPath := func(engine *Engine, path string) {
				t.Helper()
				waitSnapshotWithin(t, engine, 6*time.Second, func(s Snapshot) bool {
					for _, peer := range s.PeerTransports {
						if peer.PathType == path && peer.ActiveChannels == 1 && (peer.State == "active" || peer.State == "switching") {
							return true
						}
					}
					return false
				})
			}
			first := "hole"
			if directFirst {
				first = "direct"
			} else if turnFallback {
				first = "relay"
			}
			waitPath(b, first)
			conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			transfer := func(value string) {
				t.Helper()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.WriteString(conn, value); err != nil {
					t.Fatal(err)
				}
				data := make([]byte, len(value))
				if _, err := io.ReadFull(conn, data); err != nil || string(data) != value {
					t.Fatalf("TCP payload %q: %v", data, err)
				}
			}
			transfer(first)
			if !directFirst {
				a.mu.Lock()
				coordinator := a.run.ice
				a.mu.Unlock()
				peer := coordinator.peerList()[0]
				peer.mu.Lock()
				concurrent := peer.pending != nil && peer.parallelRelay != nil
				peer.mu.Unlock()
				if !concurrent {
					t.Fatal("relay waited for direct discovery to finish")
				}
				hibernate()
				available.Store(true)
			}
			for _, engine := range []*Engine{a, b} {
				waitPath(engine, "direct")
				waitSnapshotWithin(t, engine, 6*time.Second, func(s Snapshot) bool {
					engine.mu.Lock()
					coordinator := engine.run.ice
					engine.mu.Unlock()
					for _, peer := range coordinator.peerList() {
						peer.mu.Lock()
						finished := peer.parallelRelay == nil && peer.retiring == nil && !peer.ready.UpgradeProbe
						generation := peer.ready.TransportGeneration
						active := peer.active
						peer.mu.Unlock()
						if !finished || active == nil || peer.parallelParent.Load() != nil || active.mux.owner != peer {
							return false
						}
						coordinator.room.mu.Lock()
						selected := coordinator.room.peers[active.ready.TransportID] == active.mux
						coordinator.room.mu.Unlock()
						if !selected {
							return false
						}
						if len(s.Voice.Peers) != 1 || s.Voice.Peers[0].Generation != generation {
							return false
						}
					}
					return true
				})
			}
			transfer("direct after commit replay")
			if accepted.Load() != 1 {
				t.Fatal("path selection recreated the upstream TCP connection", accepted.Load())
			}
		})
	}
}
