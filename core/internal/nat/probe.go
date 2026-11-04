// SPDX-License-Identifier: LGPL-3.0-only
// See classify.go for provenance and modifications.
package nat

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/pion/stun/v4"
)

type Network interface {
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
	ResolveUDP(context.Context, string) (*net.UDPAddr, error)
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
	deadline := time.Now().Add(350 * time.Millisecond)
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
		var mapped stun.XORMappedAddress
		if mapped.GetFrom(m) != nil {
			continue
		}
		ip, ok := netip.AddrFromSlice(mapped.IP)
		if !ok {
			continue
		}
		return Sample{Server: sa, Source: from, Mapped: netip.AddrPortFrom(ip.Unmap(), uint16(mapped.Port)), ChangeIP: change&4 != 0, ChangePort: change&2 != 0}, nil
	}
	return Sample{}, ctx.Err()
}

func Probe(ctx context.Context, network Network, pc net.PacketConn, urls []string) (Snapshot, []netip.AddrPort) {
	var samples []Sample
	var endpoints []*net.UDPAddr
	seen := map[string]bool{}
	for _, raw := range urls {
		if ctx.Err() != nil {
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
		if s, err := Binding(ctx, pc, a, 0); err == nil {
			samples = append(samples, s)
		}
	}
	var extra *Sample
	// Reserve the extra port-change observation before filtering tests consume
	// the budget. Symmetric mapping is already established by the baseline.
	if len(endpoints) > 1 {
		fresh, err := network.ListenPacket(ctx, "udp4", "0.0.0.0:0")
		if err == nil {
			s, e := Binding(ctx, fresh, endpoints[0], 0)
			_ = fresh.Close()
			if e == nil {
				extra = &s
			}
		}
	}
	mappings := map[netip.AddrPort]bool{}
	for _, s := range samples {
		mappings[s.Mapped] = true
	}
	if len(endpoints) > 1 {
		for _, a := range endpoints {
			if len(mappings) > 1 || ctx.Err() != nil {
				break
			}
			for _, change := range []uint32{6, 2} {
				if s, err := Binding(ctx, pc, a, change); err == nil {
					// A server ignoring CHANGE-REQUEST contributes no filtering evidence.
					if s.Source != s.Server {
						if _, err := Binding(ctx, pc, net.UDPAddrFromAddrPort(s.Source), 0); err == nil {
							samples = append(samples, s)
						}
					}
				}
			}
		}
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
	if ctx.Err() != nil {
		s.Reason = "探测预算已用尽"
	}
	return s, addresses
}

func uPort(p int) string { return strconv.Itoa(p) }

var ErrNoStrategy = errors.New("当前 NAT 组合没有适用的 UDP 策略")
