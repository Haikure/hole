package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
)

func TestQUICLossCountsPersistForConnectionLifetime(t *testing.T) {
	loss := &quicPacketLoss{}
	if loss.snapshot().RatePercent != nil {
		t.Fatal("empty connection reported zero loss")
	}
	loss.record(9, 1)
	s := loss.snapshot()
	if s.Scope != "connection" || s.Samples != 10 || s.LostPackets != 1 || s.RatePercent == nil || *s.RatePercent != 10 {
		t.Fatalf("unexpected sample: %+v", s)
	}
	if s = loss.snapshot(); s.Samples != 10 || s.LostPackets != 1 || *s.RatePercent != 10 {
		t.Fatalf("idle time expired packet counts: %+v", s)
	}
	loss.record(4, 0)
	s = loss.snapshot()
	if s.Samples != 14 || s.LostPackets != 1 || s.RatePercent == nil || *s.RatePercent < 7.14 || *s.RatePercent > 7.15 {
		t.Fatalf("connection rate did not use lost / total packets: %+v", s)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			loss.record(1, 1)
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			loss.snapshot()
		}
	}()
	wg.Wait()
}

type lossCallbackFixture struct {
	congestion.CongestionControlEx
	scalar, batch int
}

func TestQUICLossCollectsBeforeFirstSnapshotAndReadsNeverReset(t *testing.T) {
	c := &observedCongestion{CongestionControlEx: &lossCallbackFixture{}, loss: &quicPacketLoss{}}
	now := monotime.Time(100 * time.Second)
	// No snapshot reader is present while the connection produces outcomes.
	c.OnCongestionEvent(1, 1200, 2400)
	c.OnCongestionEventEx(2400, now, []congestion.AckedPacketInfo{{PacketNumber: 2, BytesAcked: 1200}}, nil)
	c.OnCongestionEventEx(1200, now.Add(30*time.Second), []congestion.AckedPacketInfo{{PacketNumber: 3, BytesAcked: 1200}}, nil)
	for range 2 {
		s := c.loss.snapshot()
		if s.Samples != 3 || s.LostPackets != 1 || s.RatePercent == nil || *s.RatePercent < 33 || *s.RatePercent > 34 {
			t.Fatalf("first or repeated read lost existing outcomes: %+v", s)
		}
	}
	s := c.loss.snapshot()
	if s.Samples != 3 || s.LostPackets != 1 || s.RatePercent == nil || *s.RatePercent < 33 || *s.RatePercent > 34 {
		t.Fatalf("connection count lost older outcomes: %+v", s)
	}
}

func (f *lossCallbackFixture) OnCongestionEvent(congestion.PacketNumber, congestion.ByteCount, congestion.ByteCount) {
	f.scalar++
}
func (f *lossCallbackFixture) OnCongestionEventEx(congestion.ByteCount, monotime.Time, []congestion.AckedPacketInfo, []congestion.LostPacketInfo) {
	f.batch++
}

func TestQUICLossIgnoresECNAndMTUProbeFailures(t *testing.T) {
	f := &lossCallbackFixture{}
	c := &observedCongestion{CongestionControlEx: f, loss: &quicPacketLoss{}}
	now := monotime.Now()
	// ECN supplies no lost bytes. MTU failures appear only in the batch.
	c.OnCongestionEvent(1, 0, 1200)
	c.OnCongestionEventEx(1200, now, nil, []congestion.LostPacketInfo{{PacketNumber: 2, BytesLost: 1400}})
	if c.loss.snapshot().RatePercent != nil {
		t.Fatal("ECN / MTU failure counted as network loss")
	}
	c.OnCongestionEvent(3, 1200, 2400)
	c.OnCongestionEventEx(2400, now, []congestion.AckedPacketInfo{{PacketNumber: 4, BytesAcked: 1200}}, []congestion.LostPacketInfo{{PacketNumber: 3, BytesLost: 1200}})
	s := c.loss.snapshot()
	if s.Samples != 2 || s.LostPackets != 1 || *s.RatePercent != 50 {
		t.Fatalf("loss counted twice or missing: %+v", s)
	}
	c.OnCongestionEventEx(1200, now, []congestion.AckedPacketInfo{{PacketNumber: 5, BytesAcked: 1200}}, nil)
	if c.loss.snapshot().LostPackets != 1 || f.scalar != 2 || f.batch != 3 {
		t.Fatal("callback lost or pending count not cleared")
	}
}

func TestPeerSendLossSnapshotFollowsActivePath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux := &muxPeer{ctx: ctx, sendLoss: &quicPacketLoss{}}
	mux.dropped.Store(999) // Local receive drops must never affect network loss.
	mux.sendLoss.record(75, 25)
	p := &icePeer{coordinator: &iceCoordinator{}, active: &liveICETransport{mux: mux}, online: true}
	s := p.snapshot()
	if s.SendLoss == nil || *s.SendLoss.RatePercent != 25 || s.DroppedDatagrams != 999 {
		t.Fatalf("bad snapshot: %+v", s)
	}
	data, err := json.Marshal(s)
	if err != nil || !bytes.Contains(data, []byte(`"lost_packets":"25"`)) || !bytes.Contains(data, []byte(`"rate_percent":25`)) {
		t.Fatalf("wire stats: %s, %v", data, err)
	}
	p.active = &liveICETransport{mux: &muxPeer{ctx: ctx, sendLoss: &quicPacketLoss{}}}
	if p.snapshot().SendLoss.RatePercent != nil {
		t.Fatal("path replacement kept old samples")
	}
	cancel()
	if p.snapshot().SendLoss != nil {
		t.Fatal("closed path reported live loss")
	}
}

// Drop real encrypted QUIC packets after the handshake, below the sender.
type lossyQUICSocket struct {
	net.PacketConn
	enabled atomic.Bool
	packets atomic.Uint64
	dropped atomic.Uint64
}

func (c *lossyQUICSocket) WriteTo(data []byte, remote net.Addr) (int, error) {
	if c.enabled.Load() && len(data) > 0 && data[0]&0x80 == 0 && c.packets.Add(1)%7 == 0 {
		c.dropped.Add(1)
		return len(data), nil
	}
	return c.PacketConn.WriteTo(data, remote)
}

func TestQUICLossWithRealBBRAndDroppedPackets(t *testing.T) {
	server, address := newTestTransport(t)
	listener, err := server.Listen(mustTLS(t), iceQUICConfig("relay_udp"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	socket := &lossyQUICSocket{PacketConn: packet}
	client := &quic.Transport{Conn: socket}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	received := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(ctx)
		if err == nil {
			var stream *quic.Stream
			stream, err = conn.AcceptStream(ctx)
			if err == nil {
				_, err = io.Copy(io.Discard, stream)
			}
		}
		received <- err
	}()
	conn, err := client.Dial(ctx, address, mustTLS(t), iceQUICConfig("relay_udp"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")
	loss := useBBR(conn)
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	socket.enabled.Store(true)
	if _, err = stream.Write(bytes.Repeat([]byte("network loss"), 50000)); err != nil {
		t.Fatal(err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-received:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	s := loss.snapshot()
	if socket.dropped.Load() == 0 || s.LostPackets == 0 || s.RatePercent == nil || *s.RatePercent <= 0 || *s.RatePercent >= 100 {
		t.Fatalf("injected %d losses but reported %+v", socket.dropped.Load(), s)
	}
	t.Logf("injected=%d, detected=%d, samples=%d, rate=%.2f%%", socket.dropped.Load(), s.LostPackets, s.Samples, *s.RatePercent)
}
