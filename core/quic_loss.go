package core

import (
	"sync"

	"github.com/apernet/quic-go/congestion"
	"github.com/apernet/quic-go/monotime"
)

// SendLossSnapshot measures the local sender's QUIC loss decisions, not local
// receive-queue drops. Pending packets aren't counted until ACKed or declared
// lost. Counters cover the lifetime of the current QUIC connection. A nil rate
// means there were no outcomes yet.
// Collection runs with the connection, independently of snapshot/UI readers.
type SendLossSnapshot struct {
	Scope       string   `json:"scope"`
	Samples     uint64   `json:"samples,string"`
	LostPackets uint64   `json:"lost_packets,string"`
	RatePercent *float64 `json:"rate_percent"`
}

type quicPacketLoss struct {
	mu      sync.Mutex
	samples uint64
	lost    uint64
}

func (l *quicPacketLoss) record(acked, lost uint64) {
	if acked == 0 && lost == 0 {
		return
	}
	l.mu.Lock()
	l.samples = saturatingAdd(l.samples, saturatingAdd(acked, lost))
	l.lost = saturatingAdd(l.lost, lost)
	l.mu.Unlock()
}

func saturatingAdd(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

func (l *quicPacketLoss) snapshot() *SendLossSnapshot {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	s := lossFromCounts(l.samples, l.lost)
	l.mu.Unlock()
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
	c.loss.record(uint64(len(acked)), c.pendingLost)
	c.pendingLost = 0
	c.CongestionControlEx.OnCongestionEventEx(priorInFlight, eventTime, acked, lost)
}
