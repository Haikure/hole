package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/apernet/quic-go"
)

func TestQUICChannelLossWeightedFreshnessAndInvalidReports(t *testing.T) {
	var m quicChannelMetrics
	now := time.Now()
	local := lossFromCounts(100, 10)
	if _, total := m.lossSnapshot(now, local); total != nil {
		t.Fatal("local loss must not masquerade as whole-channel loss")
	}
	m.receiveLoss(now, lossFromCounts(900, 9))
	for range 3 {
		receive, total := m.lossSnapshot(now.Add(time.Second), local)
		if receive.Samples != 900 || total.Samples != 1000 || total.LostPackets != 19 || *total.RatePercent != 1.9 {
			t.Fatalf("not weighted by packets: receive=%+v total=%+v", receive, total)
		}
	}
	for _, invalid := range []*SendLossSnapshot{nil, {Scope: "window", Samples: 1}, {Scope: "connection", Samples: 1, LostPackets: 2}} {
		m.receiveLoss(now.Add(5*time.Second), invalid)
	}
	if receive, total := m.lossSnapshot(now.Add(quicStatsMaxAge), local); receive != nil || total != nil {
		t.Fatal("stale or malformed feedback reported as current")
	}
	m.receiveLoss(now, lossFromCounts(0, 0))
	_, total := m.lossSnapshot(now, lossFromCounts(0, 0))
	if total == nil || total.RatePercent != nil {
		t.Fatal("empty bilateral sample confused with missing feedback or zero loss")
	}
	m.receiveLoss(now, lossFromCounts(^uint64(0), 1))
	if _, total = m.lossSnapshot(now, local); total != nil {
		t.Fatal("overflow accepted")
	}
}

func TestQUICBandwidthPeaksIdleAndDelayedSampling(t *testing.T) {
	var m quicChannelMetrics
	now := time.Now()
	// Handshake traffic before the mux is ready isn't in the measured interval.
	m.startBandwidth(now, quic.ConnectionStats{BytesSent: 100, BytesReceived: 200})
	if m.bandwidthSnapshot() != nil {
		t.Fatal("reported a sample before sampling")
	}
	m.recordBandwidth(now.Add(time.Second), quic.ConnectionStats{BytesSent: 1100, BytesReceived: 200})
	m.recordBandwidth(now.Add(2*time.Second), quic.ConnectionStats{BytesSent: 1100, BytesReceived: 2200})
	s := m.bandwidthSnapshot()
	if s.Send.PeakBPS != 8000 || s.Receive.PeakBPS != 16000 || s.Total.PeakBPS != 16000 || s.Total.AverageBPS != 12000 {
		t.Fatalf("incorrect peaks or average: %+v", s)
	}
	m.recordBandwidth(now.Add(5*time.Second), quic.ConnectionStats{BytesSent: 1100, BytesReceived: 2200})
	if s = m.bandwidthSnapshot(); s.Total.AverageBPS != 12000 || s.Total.PeakBPS != 16000 || s.ElapsedSeconds != 5 {
		t.Fatalf("idle time reduced average: %+v", s)
	}
	m.recordBandwidth(now.Add(8*time.Second), quic.ConnectionStats{BytesSent: 4100, BytesReceived: 2200})
	if s = m.bandwidthSnapshot(); s.Send.PeakBPS != 8000 || s.Total.AverageBPS != 9600 {
		t.Fatalf("delayed tick inflated bandwidth: %+v", s)
	}
	s.Send.PeakBPS = 0
	if m.bandwidthSnapshot().Send.PeakBPS != 8000 {
		t.Fatal("snapshot reader mutated metrics")
	}
}

func TestQUICMetricsConcurrentReadersAndWriters(t *testing.T) {
	var m quicChannelMetrics
	now := time.Now()
	m.startBandwidth(now, quic.ConnectionStats{})
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				if i == 0 {
					m.recordBandwidth(now.Add(time.Duration(j+1)*time.Second), quic.ConnectionStats{BytesSent: uint64(j * 100)})
				} else if i == 1 {
					m.receiveLoss(now, lossFromCounts(100, 1))
				} else {
					m.lossSnapshot(now, lossFromCounts(10, 1))
					m.bandwidthSnapshot()
				}
			}
		}()
	}
	wg.Wait()
}

func TestQUICMetricsFollowActivePath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	mux := &muxPeer{ctx: ctx, sendLoss: &quicPacketLoss{}}
	mux.sendLoss.record(90, 10)
	mux.metrics.receiveLoss(now, lossFromCounts(900, 9))
	mux.metrics.startBandwidth(now.Add(-time.Second), quic.ConnectionStats{})
	mux.metrics.recordBandwidth(now, quic.ConnectionStats{BytesSent: 1000})
	p := &icePeer{coordinator: &iceCoordinator{}, online: true, active: &liveICETransport{mux: mux}}
	if s := p.snapshot(); s.QUICLoss == nil || s.ReceiveLoss == nil || s.Bandwidth == nil {
		t.Fatalf("missing metrics: %+v", s)
	}
	p.active = &liveICETransport{mux: &muxPeer{ctx: ctx, sendLoss: &quicPacketLoss{}}}
	if s := p.snapshot(); s.QUICLoss != nil || s.ReceiveLoss != nil || s.Bandwidth != nil {
		t.Fatal("new path inherited old telemetry")
	}
	p.active.mux = mux
	cancel()
	if s := p.snapshot(); s.QUICLoss != nil || s.ReceiveLoss != nil || s.Bandwidth != nil {
		t.Fatal("closed path reported live metrics")
	}
}

// Exercise the actual HMX control stream without any Worker or signaling server.
func TestQUICStatsControlStreamAndLegacyPeer(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "legacy"
		if enabled {
			name = "bilateral"
		}
		t.Run(name, func(t *testing.T) {
			server, address := newTestTransport(t)
			listener, err := server.Listen(mustTLS(t), iceQUICConfig("direct"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client, _ := newTestTransport(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type result struct {
				peer *muxPeer
				err  error
			}
			accepted := make(chan result, 1)
			go func() {
				conn, err := listener.Accept(ctx)
				if err != nil {
					accepted <- result{err: err}
					return
				}
				owner := &icePeer{coordinator: &iceCoordinator{runtimeID: "server-runtime", desired: Request{Config: Config{DeviceName: "server"}}}}
				ready := iceSignalMessage{TransportID: "stats-test", TransportGeneration: 1, PeerRuntimeID: "client-runtime", InitiatorID: "client"}
				peer, err := newMuxPeer(ctx, owner, conn, ready)
				accepted <- result{peer, err}
			}()
			conn, err := client.Dial(ctx, address, mustTLS(t), iceQUICConfig("direct"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseWithError(0, "done")
			stream, err := conn.OpenStreamSync(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.SetDeadline(time.Now().Add(4 * time.Second))
			if err = writeMuxPrefix(stream, muxControl, 0); err != nil {
				t.Fatal(err)
			}
			hello := muxHello{TransportID: "stats-test", Generation: 1, LocalRuntime: "client-runtime", RemoteRuntime: "server-runtime", Version: sessionProtocol, QUICStats: enabled}
			if err = writeSessionJSON(stream, hello); err != nil {
				t.Fatal(err)
			}
			var remote muxHello
			if err = readSessionJSON(stream, &remote); err != nil || !remote.QUICStats {
				t.Fatalf("hello=%+v err=%v", remote, err)
			}
			var peer *muxPeer
			select {
			case got := <-accepted:
				if got.err != nil {
					t.Fatal(got.err)
				}
				peer = got.peer
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			defer peer.close()
			if enabled {
				var report mappingControl
				if err = readSessionJSON(stream, &report); err != nil || report.Type != "QUIC_STATS" || report.SendLoss == nil {
					t.Fatalf("report=%+v err=%v", report, err)
				}
				if err = writeSessionJSON(stream, mappingControl{Type: "QUIC_STATS", SendLoss: lossFromCounts(100, 7)}); err != nil {
					t.Fatal(err)
				}
			} else {
				// An old peer rejects unknown messages: it must receive no report.
				_ = stream.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				var report mappingControl
				if err = readSessionJSON(stream, &report); err == nil {
					t.Fatalf("legacy received %+v", report)
				}
			}
			for {
				receive, _ := peer.metrics.lossSnapshot(time.Now(), peer.sendLoss.snapshot())
				bandwidth := peer.metrics.bandwidthSnapshot()
				if bandwidth != nil && (!enabled || (receive != nil && receive.LostPackets == 7)) {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("metrics did not arrive over control stream")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if peer.ctx.Err() != nil {
				t.Fatal("control stream stopped")
			}
		})
	}
}
