package core

import (
	"sync"
	"time"

	"github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
)

const packetLossWindowSeconds = 60

// SendLossSnapshot measures the local sender's QUIC loss decisions, not local
// receive-queue drops. Pending packets aren't counted until ACKed or declared
// lost. A nil rate means there were no outcomes in the recent window.
// Collection runs with the connection, independently of snapshot/UI readers.
type SendLossSnapshot struct {
	WindowSeconds int      `json:"window_seconds"`
	Samples       uint64   `json:"samples,string"`
	LostPackets   uint64   `json:"lost_packets,string"`
	RatePercent   *float64 `json:"rate_percent"`
}

type packetLossBucket struct {
	second int64
	acked  uint64
	lost   uint64
}

type quicPacketLoss struct {
	mu      sync.Mutex
	buckets [packetLossWindowSeconds]packetLossBucket
}

func (l *quicPacketLoss) record(now monotime.Time, acked, lost uint64) {
	if acked == 0 && lost == 0 {
		return
	}
	second := int64(now) / int64(time.Second)
	l.mu.Lock()
	b := &l.buckets[second%packetLossWindowSeconds]
	if b.second != second {
		*b = packetLossBucket{second: second}
	}
	b.acked += acked
	b.lost += lost
	l.mu.Unlock()
}

func (l *quicPacketLoss) snapshot(now monotime.Time) *SendLossSnapshot {
	if l == nil {
		return nil
	}
	second := int64(now) / int64(time.Second)
	s := &SendLossSnapshot{WindowSeconds: packetLossWindowSeconds}
	l.mu.Lock()
	for _, b := range l.buckets {
		if b.second <= second && second-b.second < packetLossWindowSeconds {
			s.Samples += b.acked + b.lost
			s.LostPackets += b.lost
		}
	}
	l.mu.Unlock()
	if s.Samples > 0 {
		rate := float64(s.LostPackets) / float64(s.Samples) * 100
		s.RatePercent = &rate
	}
	return s
}

// The pinned quic-go only updates ConnectionStats.PacketsLost in Cubic, so it
// cannot be used with our BBR controller. Observe the same loss decisions while
// forwarding every congestion callback unchanged. QUIC calls the scalar loss
// callback only for non-MTU-probe losses (zero lostBytes denotes ECN); its batch
// callback includes MTU probe losses, so counting that slice would overreport.
type observedCongestion struct {
	congestion.CongestionControlEx
	loss        *quicPacketLoss
	pendingLost uint64 // owned by QUIC's connection event loop
}

func (c *observedCongestion) OnCongestionEvent(number congestion.PacketNumber, lostBytes, priorInFlight congestion.ByteCount) {
	if lostBytes > 0 {
		c.pendingLost++
	}
	c.CongestionControlEx.OnCongestionEvent(number, lostBytes, priorInFlight)
}

func (c *observedCongestion) OnCongestionEventEx(priorInFlight congestion.ByteCount, eventTime monotime.Time, acked []congestion.AckedPacketInfo, lost []congestion.LostPacketInfo) {
	c.loss.record(eventTime, uint64(len(acked)), c.pendingLost)
	c.pendingLost = 0
	c.CongestionControlEx.OnCongestionEventEx(priorInFlight, eventTime, acked, lost)
}
