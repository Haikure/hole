package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hole/core"
)

// NetworkBinding is additive to API 1. A handle is a decimal string, never a
// floating-point JSON number. BindSocket borrows fd only for this synchronous
// call: the host may duplicate it to use a public API, but must not close the
// original descriptor. Missing/stale handles fail closed, without default-route
// or global DNS fallback. Calls run on background threads, not the UI thread.
type NetworkBinding interface {
	InterfacesJSON() (string, error)
	NetworkJSON() (string, error)
	BindSocket(fd int64, networkHandle string) error
	LookupIP(host, networkHandle string) (string, error)
}

func NewEngineWithNetworkBinding(events EventSink, binding NetworkBinding) (*Engine, error) {
	if binding == nil {
		return nil, bridgeError(fmt.Errorf("network binding is required"))
	}
	p := &networkPlatform{provider: binding, binding: binding, bindingEnabled: true, lookupSlots: make(chan struct{}, 4)}
	e := newEngine(events, p)
	e.network = p
	return e, nil
}

func (p *networkPlatform) NetworkBinding() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bindingEnabled || p.binding != nil
}

func (p *networkPlatform) SnapshotNetwork(ctx context.Context) (core.Platform, core.NetworkSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, core.NetworkSnapshot{}, err
	}
	p.mu.Lock()
	binding := p.binding
	closed := p.provider == nil
	p.mu.Unlock()
	if closed {
		return nil, core.NetworkSnapshot{}, core.ErrClosed
	}
	if binding == nil {
		return p, core.NetworkSnapshot{}, nil
	}
	text, err := binding.NetworkJSON()
	var network core.NetworkSnapshot
	if err == nil {
		if len(text) > 128*1024 {
			err = errors.New("network snapshot exceeds 128 KiB")
		} else {
			err = json.Unmarshal([]byte(text), &network)
		}
	}
	if err != nil {
		return nil, network, &core.Fault{Code: "network_snapshot_failed", Message: err.Error()}
	}
	if !network.Available || network.Handle == "" || network.Handle == "0" {
		return nil, network, &core.Fault{Code: "no_network", Message: "等待可用的 Wi-Fi、移动或以太网络"}
	}
	return &boundPlatform{root: p, binding: binding, network: network}, network, nil
}

type boundPlatform struct {
	root    *networkPlatform
	binding NetworkBinding
	network core.NetworkSnapshot
}

func (p *boundPlatform) InterfaceSnapshots(ctx context.Context) ([]core.InterfaceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []core.InterfaceSnapshot{{Name: p.network.Interface, Up: true, Addresses: append([]string{}, p.network.Addresses...)}}, nil
}
func (p *boundPlatform) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	values, err := p.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, v := range values {
		if ip := net.ParseIP(v); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

func (p *boundPlatform) Candidates(ctx context.Context, cfg core.Config, port int) ([]core.Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return core.CandidatesFromInterfaces(cfg, port, []core.InterfaceSnapshot{{Name: p.network.Interface, Up: true, Addresses: p.network.Addresses}})
}

func (p *boundPlatform) control(_, _ string, raw syscall.RawConn) error {
	p.root.mu.Lock()
	closed := p.root.provider == nil
	p.root.mu.Unlock()
	if closed {
		return core.ErrClosed
	}
	var bindErr error
	err := raw.Control(func(fd uintptr) { bindErr = p.binding.BindSocket(int64(fd), p.network.Handle) })
	if err != nil {
		return err
	}
	if bindErr != nil {
		return &core.Fault{Code: "network_socket_bind_failed", Message: fmt.Sprintf("绑定所选网络失败：%v", bindErr)}
	}
	return nil
}

func (p *boundPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return (&net.ListenConfig{Control: p.control}).ListenPacket(ctx, network, address)
}

// Android's resolver is synchronous on API 26. Bound outstanding JNI lookups to
// four and let Go cancellation return immediately; their results are discarded
// after cancellation. The platform resolver owns its own finite DNS timeout.
func (p *boundPlatform) lookup(ctx context.Context, host string) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []string{ip.String()}, nil
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return []string{"127.0.0.1", "::1"}, nil
	}
	select {
	case p.root.lookupSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	type answer struct {
		text string
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		defer func() { <-p.root.lookupSlots }()
		text, err := p.binding.LookupIP(host, p.network.Handle)
		done <- answer{text, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if result.err != nil {
			return nil, &core.Fault{Code: "network_dns_failed", Message: fmt.Sprintf("所选网络 DNS 解析失败：%v", result.err)}
		}
		var values []string
		if len(result.text) > 16*1024 || json.Unmarshal([]byte(result.text), &values) != nil {
			return nil, errors.New("invalid platform DNS result")
		}
		ips := make([]string, 0, min(len(values), 8))
		for _, value := range values {
			if ip := net.ParseIP(value); ip != nil {
				ips = append(ips, ip.String())
			}
			if len(ips) == 8 {
				break
			}
		}
		if len(ips) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
		}
		return ips, nil
	}
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") || net.ParseIP(host).IsLoopback()
}

func (p *boundPlatform) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := p.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result, len(ips))
	for index, ip := range ips {
		go func(index int, ip string) {
			timer := time.NewTimer(time.Duration(index) * 250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			case <-timer.C:
			}
			conn, err := (&net.Dialer{Timeout: 10 * time.Second, Control: p.control}).DialContext(ctx, network, net.JoinHostPort(ip, port))
			results <- result{conn, err}
		}(index, ip)
	}
	defer cancel()
	var last error
	for remaining := len(ips); remaining > 0; remaining-- {
		got := <-results
		if got.err == nil {
			cancel()
			for i := 1; i < remaining; i++ {
				extra := <-results
				if extra.conn != nil {
					extra.conn.Close()
				}
			}
			return got.conn, nil
		}
		last = got.err
	}
	return nil, last
}

func (p *boundPlatform) DialSignal(ctx context.Context, network, address string) (net.Conn, error) {
	return p.dial(ctx, network, address)
}
func (p *boundPlatform) DialTCP(ctx context.Context, address string) (*net.TCPConn, error) {
	if loopbackAddress(address) {
		return (core.DefaultPlatform{}).DialTCP(ctx, address)
	}
	conn, err := p.dial(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}
func (p *boundPlatform) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid UDP port")
	}
	ips, err := p.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	return &net.UDPAddr{IP: net.ParseIP(ips[0]), Port: port}, nil
}
func (p *boundPlatform) DialUDP(ctx context.Context, address *net.UDPAddr) (*net.UDPConn, error) {
	if address.IP.IsLoopback() {
		return (core.DefaultPlatform{}).DialUDP(ctx, address)
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second, Control: p.control}).DialContext(ctx, "udp", address.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

func (p *networkPlatform) DialTCP(ctx context.Context, address string) (*net.TCPConn, error) {
	if !p.NetworkBinding() || loopbackAddress(address) {
		return p.DefaultPlatform.DialTCP(ctx, address)
	}
	selected, _, err := p.SnapshotNetwork(ctx)
	if err != nil {
		return nil, err
	}
	return selected.DialTCP(ctx, address)
}
func (p *networkPlatform) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	if !p.NetworkBinding() || loopbackAddress(address) {
		return p.DefaultPlatform.ResolveUDP(ctx, address)
	}
	selected, _, err := p.SnapshotNetwork(ctx)
	if err != nil {
		return nil, err
	}
	return selected.ResolveUDP(ctx, address)
}
func (p *networkPlatform) DialUDP(ctx context.Context, address *net.UDPAddr) (*net.UDPConn, error) {
	if !p.NetworkBinding() || address.IP.IsLoopback() {
		return p.DefaultPlatform.DialUDP(ctx, address)
	}
	selected, _, err := p.SnapshotNetwork(ctx)
	if err != nil {
		return nil, err
	}
	return selected.DialUDP(ctx, address)
}
