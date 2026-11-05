// SPDX-License-Identifier: LGPL-3.0-only
// See classify.go for provenance and modifications.
package nat

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/pion/stun/v4"
)

type Network interface {
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
	ResolveUDP(context.Context, string) (*net.UDPAddr, error)
}

const probeResponseTimeout = 3 * time.Second
const probeRequestRepeats = 2
const maxProbeServers = 8

type stunResponse struct {
	message *stun.Message
	source  net.Addr
}

// stunDispatcher gives concurrent requests on one socket independent response
// channels. NAT mapping classification depends on reusing this same socket.
type stunDispatcher struct {
	pc      net.PacketConn
	mu      sync.Mutex
	pending map[string]chan stunResponse
	read    chan struct{}
	once    sync.Once
}

func newSTUNDispatcher(pc net.PacketConn) *stunDispatcher {
	d := &stunDispatcher{pc: pc, pending: map[string]chan stunResponse{}, read: make(chan struct{})}
	go d.readLoop()
	return d
}

func (d *stunDispatcher) readLoop() {
	defer close(d.read)
	buf := make([]byte, 2048)
	for {
		n, source, err := d.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		m := new(stun.Message)
		m.Raw = append(m.Raw, buf[:n]...)
		if m.Decode() != nil || m.Type != stun.BindingSuccess {
			continue
		}
		id := string(m.TransactionID[:])
		d.mu.Lock()
		ch := d.pending[id]
		d.mu.Unlock()
		if ch != nil {
			select {
			case ch <- stunResponse{message: m, source: source}:
			default:
			}
		}
	}
}

func (d *stunDispatcher) close() {
	d.once.Do(func() {
		_ = d.pc.SetReadDeadline(time.Now())
		<-d.read
		_ = d.pc.SetReadDeadline(time.Time{})
	})
}

func mappedAddress(m *stun.Message) (netip.AddrPort, bool) {
	var ip net.IP
	var port int
	var xor stun.XORMappedAddress
	if err := xor.GetFrom(m); err == nil {
		ip, port = xor.IP, xor.Port
	} else {
		var mapped stun.MappedAddress
		if err := mapped.GetFrom(m); err != nil {
			return netip.AddrPort{}, false
		}
		ip, port = mapped.IP, mapped.Port
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok || port < 1 || port > 65535 {
		return netip.AddrPort{}, false
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addr, uint16(port)), true
}

func (d *stunDispatcher) binding(ctx context.Context, server *net.UDPAddr, change uint32) (Sample, error) {
	requests := make([]*stun.Message, probeRequestRepeats)
	responses := make(chan stunResponse, probeRequestRepeats)
	ids := make([]string, 0, probeRequestRepeats)
	for i := range requests {
		request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
		if change != 0 {
			request.Add(stun.AttrChangeRequest, []byte{0, 0, 0, byte(change)})
		}
		requests[i] = request
		ids = append(ids, string(request.TransactionID[:]))
	}
	d.mu.Lock()
	for _, id := range ids {
		d.pending[id] = responses
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		for _, id := range ids {
			delete(d.pending, id)
		}
		d.mu.Unlock()
	}()
	for _, request := range requests {
		if ctx.Err() != nil {
			return Sample{}, ctx.Err()
		}
		if _, err := d.pc.WriteTo(request.Raw, server); err != nil {
			return Sample{}, err
		}
	}
	timer := time.NewTimer(probeResponseTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return Sample{}, ctx.Err()
		case <-timer.C:
			return Sample{}, context.DeadlineExceeded
		case response := <-responses:
			from := addrPort(response.source)
			if change == 0 && from != addrPort(server) {
				continue
			}
			mapped, ok := mappedAddress(response.message)
			if !ok {
				continue
			}
			return Sample{Server: addrPort(server), Source: from, Mapped: mapped, ChangeIP: change&4 != 0, ChangePort: change&2 != 0}, nil
		}
	}
}

func probeBatch(ctx context.Context, dispatcher *stunDispatcher, servers []*net.UDPAddr, changes ...uint32) []Sample {
	results := make(chan Sample, len(servers)*len(changes))
	var wg sync.WaitGroup
	for _, server := range servers {
		for _, change := range changes {
			wg.Add(1)
			go func(server *net.UDPAddr, change uint32) {
				defer wg.Done()
				if sample, err := dispatcher.binding(ctx, server, change); err == nil {
					results <- sample
				}
			}(server, change)
		}
	}
	wg.Wait()
	close(results)
	samples := make([]Sample, 0, len(results))
	for sample := range results {
		samples = append(samples, sample)
	}
	return samples
}

func confirmChangedResponses(ctx context.Context, dispatcher *stunDispatcher, changed []Sample) []Sample {
	servers := make([]*net.UDPAddr, 0, len(changed))
	seen := map[netip.AddrPort]bool{}
	for _, sample := range changed {
		if sample.Source == sample.Server || seen[sample.Source] {
			continue
		}
		seen[sample.Source] = true
		servers = append(servers, net.UDPAddrFromAddrPort(sample.Source))
	}
	confirmed := map[netip.AddrPort]bool{}
	for _, sample := range probeBatch(ctx, dispatcher, servers, 0) {
		confirmed[sample.Server] = true
	}
	valid := make([]Sample, 0, len(changed))
	for _, sample := range changed {
		if confirmed[sample.Source] {
			valid = append(valid, sample)
		}
	}
	return valid
}

func addrPort(a net.Addr) netip.AddrPort {
	if a == nil {
		return netip.AddrPort{}
	}
	v, _ := netip.ParseAddrPort(a.String())
	return v
}

// Binding owns the socket reader until it returns. Transaction and actual
// response source checks discard reordered, late and unrelated datagrams.
func Binding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change uint32) (Sample, error) {
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if change != 0 {
		request.Add(stun.AttrChangeRequest, []byte{0, 0, 0, byte(change)})
	}
	deadline := time.Now().Add(probeResponseTimeout)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = pc.SetDeadline(deadline)
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
		}
		_ = pc.SetDeadline(time.Time{})
	}()
	if _, err := pc.WriteTo(request.Raw, server); err != nil {
		return Sample{}, err
	}
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		n, source, err := pc.ReadFrom(buf)
		if err != nil {
			return Sample{}, err
		}
		m := new(stun.Message)
		m.Raw = append(m.Raw, buf[:n]...)
		if m.Decode() != nil || m.TransactionID != request.TransactionID || m.Type != stun.BindingSuccess {
			continue
		}
		sa, from := addrPort(server), addrPort(source)
		if change == 0 && sa != from {
			continue
		}
		// The unpredictable transaction binds the response to this request.
		// Probe confirms a changed endpoint using an ordinary binding before
		// using it as classification evidence.
		mapped, ok := mappedAddress(m)
		if !ok {
			continue
		}
		return Sample{Server: sa, Source: from, Mapped: mapped, ChangeIP: change&4 != 0, ChangePort: change&2 != 0}, nil
	}
	return Sample{}, ctx.Err()
}

func Probe(ctx context.Context, network Network, pc net.PacketConn, urls []string) (Snapshot, []netip.AddrPort) {
	var endpoints []*net.UDPAddr
	seen := map[string]bool{}
	for _, raw := range urls {
		if ctx.Err() != nil || len(endpoints) >= maxProbeServers {
			break
		}
		u, err := stun.ParseURI(raw)
		if err != nil {
			continue
		}
		a, err := network.ResolveUDP(ctx, net.JoinHostPort(u.Host, uPort(u.Port)))
		if err != nil || a.IP.To4() == nil || seen[a.String()] {
			continue
		}
		seen[a.String()] = true
		endpoints = append(endpoints, a)
	}
	dispatcher := newSTUNDispatcher(pc)
	defer dispatcher.close()
	samples := probeBatch(ctx, dispatcher, endpoints, 0)
	mappings := map[netip.AddrPort]bool{}
	for _, s := range samples {
		mappings[s.Mapped] = true
	}
	var extra *Sample
	var extraDone <-chan *Sample
	if len(mappings) > 1 && len(samples) > 0 && len(endpoints) > 1 {
		result := make(chan *Sample, 1)
		extraDone = result
		server := samples[0].Server
		go func() {
			defer close(result)
			fresh, err := network.ListenPacket(ctx, "udp4", "0.0.0.0:0")
			if err != nil {
				result <- nil
				return
			}
			defer fresh.Close()
			freshDispatcher := newSTUNDispatcher(fresh)
			defer freshDispatcher.close()
			sample, err := freshDispatcher.binding(ctx, net.UDPAddrFromAddrPort(server), 0)
			if err != nil {
				result <- nil
				return
			}
			result <- &sample
		}()
	}
	changed := probeBatch(ctx, dispatcher, endpoints, 6, 2)
	samples = append(samples, confirmChangedResponses(ctx, dispatcher, changed)...)
	if extraDone != nil {
		extra = <-extraDone
	}
	s := Classify(addrPort(pc.LocalAddr()), samples, extra)
	addresses := []netip.AddrPort{}
	for _, v := range samples {
		found := false
		for _, a := range addresses {
			found = found || a == v.Mapped
		}
		if !found {
			addresses = append(addresses, v.Mapped)
		}
	}
	if ctx.Err() != nil && s.Status == "insufficient" {
		s.Reason = "探测预算已用尽"
	}
	return s, addresses
}

func uPort(p int) string { return strconv.Itoa(p) }

var ErrNoStrategy = errors.New("当前 NAT 组合没有适用的 UDP 策略")
