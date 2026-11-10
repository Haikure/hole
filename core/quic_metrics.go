package core

import (
	"sync"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/monotime"
)

const (
	quicStatsInterval = 2 * time.Second
	quicStatsMaxAge   = 6 * time.Second
)

// BandwidthRates describes actual QUIC usage in bits/s, including retransmits
// and control traffic, excluding UDP/IP and relay encapsulation.
type BandwidthRates struct {
	PeakBPS    float64 `json:"peak_bps"`
	AverageBPS float64 `json:"average_bps"`
}

type BandwidthSnapshot struct {
	SampleSeconds  int            `json:"sample_seconds"`
	ElapsedSeconds float64        `json:"elapsed_seconds"`
	Send           BandwidthRates `json:"send"`
	Receive        BandwidthRates `json:"receive"`
	Total          BandwidthRates `json:"total"`
}

// Owned by one mux/QUIC connection. Readers never advance sampling or reset it.
type quicChannelMetrics struct {
	mu             sync.Mutex
	remoteLoss     *SendLossSnapshot
	remoteAt       time.Time
	started, last  time.Time
	base, previous quic.ConnectionStats
	bandwidth      *BandwidthSnapshot
}

func lossFromCounts(samples, lost uint64) *SendLossSnapshot {
	s := &SendLossSnapshot{WindowSeconds: packetLossWindowSeconds, Samples: samples, LostPackets: lost}
	if samples != 0 {
		rate := float64(lost) / float64(samples) * 100
		s.RatePercent = &rate
	}
	return s
}

func (m *quicChannelMetrics) receiveLoss(now time.Time, loss *SendLossSnapshot) {
	if loss == nil || loss.WindowSeconds != packetLossWindowSeconds || loss.LostPackets > loss.Samples {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// The sender's counters are authoritative, never its rounded percentage.
	m.remoteLoss = lossFromCounts(loss.Samples, loss.LostPackets)
	m.remoteAt = now
}

func (m *quicChannelMetrics) lossSnapshot(now time.Time, local *SendLossSnapshot) (receive, total *SendLossSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.remoteLoss == nil || now.Before(m.remoteAt) || now.Sub(m.remoteAt) >= quicStatsMaxAge {
		return nil, nil
	}
	receive = lossFromCounts(m.remoteLoss.Samples, m.remoteLoss.LostPackets)
	if local == nil || ^uint64(0)-local.Samples < receive.Samples {
		return receive, nil
	}
	total = lossFromCounts(local.Samples+receive.Samples, local.LostPackets+receive.LostPackets)
	return receive, total
}

func (m *quicChannelMetrics) startBandwidth(now time.Time, stats quic.ConnectionStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started, m.last, m.base, m.previous = now, now, stats, stats
}

func (m *quicChannelMetrics) recordBandwidth(now time.Time, stats quic.ConnectionStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seconds := now.Sub(m.last).Seconds()
	if m.started.IsZero() || seconds <= 0 || stats.BytesSent < m.previous.BytesSent || stats.BytesReceived < m.previous.BytesReceived {
		return
	}
	if m.bandwidth == nil {
		m.bandwidth = &BandwidthSnapshot{SampleSeconds: 1}
	}
	s := m.bandwidth
	s.ElapsedSeconds = now.Sub(m.started).Seconds()
	up := float64(stats.BytesSent-m.previous.BytesSent) * 8 / seconds
	down := float64(stats.BytesReceived-m.previous.BytesReceived) * 8 / seconds
	s.Send.PeakBPS = max(s.Send.PeakBPS, up)
	s.Receive.PeakBPS = max(s.Receive.PeakBPS, down)
	// Peaks of the two directions can occur in different intervals.
	s.Total.PeakBPS = max(s.Total.PeakBPS, up+down)
	s.Send.AverageBPS = float64(stats.BytesSent-m.base.BytesSent) * 8 / s.ElapsedSeconds
	s.Receive.AverageBPS = float64(stats.BytesReceived-m.base.BytesReceived) * 8 / s.ElapsedSeconds
	s.Total.AverageBPS = s.Send.AverageBPS + s.Receive.AverageBPS
	m.last, m.previous = now, stats
}

func (m *quicChannelMetrics) bandwidthSnapshot() *BandwidthSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bandwidth == nil {
		return nil
	}
	s := *m.bandwidth
	return &s
}

func (p *muxPeer) sampleBandwidth() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			// Use the actual sampling time if scheduling was delayed.
			p.metrics.recordBandwidth(time.Now(), p.conn.ConnectionStats())
		}
	}
}

func (p *muxPeer) reportQUICStats() {
	ticker := time.NewTicker(quicStatsInterval)
	defer ticker.Stop()
	for {
		if p.writeControl(mappingControl{Type: "QUIC_STATS", SendLoss: p.sendLoss.snapshot(monotime.Now())}) != nil {
			p.stop()
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
