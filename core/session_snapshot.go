package core

import "sync/atomic"

type tcpTraffic struct {
	read    atomic.Uint64
	written atomic.Uint64
}

type mappingStats struct {
	tcp        uint64
	udp        uint64
	active     bool
	read       uint64
	written    uint64
	tcpRead    uint64
	tcpWritten uint64
	buffered   uint64
}

// tcpTraffic is owned by the mapping rather than an individual session, so a
// completed TCP connection cannot make the mapping's application totals drop
// back to zero.
func (m *sessionManager) tcpTrafficLocked(mapping string) *tcpTraffic {
	traffic := m.tcpBytes[mapping]
	if traffic == nil {
		traffic = &tcpTraffic{}
		m.tcpBytes[mapping] = traffic
	}
	return traffic
}

func (m *sessionManager) snapshot() map[string]mappingStats {
	result := make(map[string]mappingStats)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return result
	}
	for id, traffic := range m.tcpBytes {
		stats := result[id]
		stats.read += traffic.read.Load()
		stats.written += traffic.written.Load()
		result[id] = stats
	}
	for id, client := range m.clients {
		stats := result[id]
		client.mu.Lock()
		for _, session := range client.tcp {
			session.mu.Lock()
			if !session.closed {
				stats.tcp++
				read := session.txBase + uint64(len(session.tx))
				written := session.rxNext
				stats.tcpRead += read
				stats.tcpWritten += written
				stats.buffered += uint64(len(session.tx))
			}
			session.mu.Unlock()
		}
		stats.udp += uint64(len(client.udpByID))
		stats.read += client.udpReadBytes.Load()
		stats.written += client.udpWrittenBytes.Load()
		if client.t.protocol == "udp" {
			stats.active = client.udpLink != nil && client.udpLink.ctx.Err() == nil
		} else {
			stats.active = client.transport != nil && client.transport.ctx.Err() == nil && client.transport.conn.Context().Err() == nil
		}
		client.mu.Unlock()
		result[id] = stats
	}
	for key, entry := range m.tcp {
		select {
		case <-entry.ready:
			if entry.session != nil {
				session := entry.session
				stats := result[key.mapping]
				session.mu.Lock()
				if !session.closed {
					stats.tcp++
					read := session.txBase + uint64(len(session.tx))
					written := session.rxNext
					stats.tcpRead += read
					stats.tcpWritten += written
					stats.buffered += uint64(len(session.tx))
					stats.active = stats.active || (session.link != nil && session.link.ctx.Err() == nil)
				}
				session.mu.Unlock()
				result[key.mapping] = stats
			}
		default:
		}
	}
	for key, group := range m.udp {
		stats := result[key.mapping]
		group.mu.Lock()
		stats.udp += uint64(len(group.sessions))
		stats.read += group.udpReadBytes.Load()
		stats.written += group.udpWrittenBytes.Load()
		stats.active = stats.active || (group.link != nil && group.link.ctx.Err() == nil)
		group.mu.Unlock()
		result[key.mapping] = stats
	}
	return result
}
