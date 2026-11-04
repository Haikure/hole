package nat

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type igdFixture struct {
	pmpFixture
	muIGD    sync.Mutex
	actions  []string
	conflict bool
}
type igdPacket struct{ net.PacketConn }

func (p igdPacket) WriteTo(b []byte, a net.Addr) (int, error) {
	if strings.HasPrefix(string(b), "M-SEARCH") {
		return p.PacketConn.WriteTo([]byte{0, 0}, a)
	}
	return p.PacketConn.WriteTo(b, a)
}
func (p igdPacket) ReadFrom(b []byte) (int, net.Addr, error) {
	_, _, err := p.PacketConn.ReadFrom(b)
	if err != nil {
		return 0, nil, err
	}
	return copy(b, []byte("HTTP/1.1 200 OK\r\nLOCATION: http://192.0.2.1/root.xml\r\n\r\n")), net.UDPAddrFromAddrPort(ap("192.0.2.1:1900")), nil
}
func (r *igdFixture) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	pc, e := r.pmpFixture.ListenPacket(ctx, network, address)
	return igdPacket{pc}, e
}
func (r *igdFixture) DialUDP(ctx context.Context, _ *net.UDPAddr) (*net.UDPConn, error) {
	c, e := (&net.Dialer{}).DialContext(ctx, "udp4", "127.0.0.1:9")
	if e != nil {
		return nil, e
	}
	return c.(*net.UDPConn), nil
}
func (r *igdFixture) DialSignal(ctx context.Context, _, _ string) (net.Conn, error) {
	left, right := net.Pipe()
	go func() {
		defer right.Close()
		req, e := http.ReadRequest(bufio.NewReader(right))
		if e != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		action := req.Header.Get("SOAPAction")
		action = strings.Trim(action, "\"")
		_, action, _ = strings.Cut(action, "#")
		r.muIGD.Lock()
		r.actions = append(r.actions, action)
		conflict := r.conflict
		r.muIGD.Unlock()
		status := 200
		body := `<root><device><serviceList><service><serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`
		switch action {
		case "AddPortMapping":
			body = "<ok/>"
			if conflict {
				status = 500
			}
		case "GetExternalIPAddress":
			body = "<reply><NewExternalIPAddress>198.51.100.6</NewExternalIPAddress></reply>"
		case "GetSpecificPortMappingEntry":
			body = "<reply><NewInternalClient>127.0.0.1</NewInternalClient><NewInternalPort>40000</NewInternalPort><NewLeaseDuration>90</NewLeaseDuration></reply>"
		case "DeletePortMapping":
			body = "<ok/>"
		}
		_, _ = fmt.Fprintf(right, "HTTP/1.1 %d Fixture\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, len(body), body)
	}()
	return left, nil
}

func TestIGDGrantedLeaseRenewalConflictAndDeletion(t *testing.T) {
	r := &igdFixture{}
	m, e := mapIGD(context.Background(), r, netip.MustParseAddr("192.0.2.1"), 40000)
	if e != nil {
		t.Fatal(e)
	}
	if m.Snapshot.Address != "198.51.100.6:40000" || m.Snapshot.LeaseSeconds != 90 {
		t.Fatal(m.Snapshot)
	}
	if _, lease, e := m.update(context.Background(), 300); e != nil || lease != 90 {
		t.Fatalf("renew %d %v", lease, e)
	}
	if _, _, e = m.update(context.Background(), 0); e != nil {
		t.Fatal(e)
	}
	r.muIGD.Lock()
	last := r.actions[len(r.actions)-1]
	r.conflict = true
	r.muIGD.Unlock()
	if last != "DeletePortMapping" {
		t.Fatal(last)
	}
	if _, e = mapIGD(context.Background(), r, netip.MustParseAddr("192.0.2.1"), 40000); e == nil {
		t.Fatal("IGD conflict ignored")
	}
}

// In-memory router: automated mapping tests never contact the host router.
type pmpFixture struct {
	mu       sync.Mutex
	leases   []uint32
	external uint16
	fail     bool
}
type fixturePacket struct {
	r        *pmpFixture
	response chan []byte
	done     chan struct{}
	once     sync.Once
}

func (r *pmpFixture) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	return &fixturePacket{r: r, response: make(chan []byte, 1), done: make(chan struct{})}, nil
}
func (r *pmpFixture) ResolveUDP(context.Context, string) (*net.UDPAddr, error) {
	return nil, errors.New("unexpected DNS")
}
func (p *fixturePacket) WriteTo(b []byte, _ net.Addr) (int, error) {
	response := make([]byte, 12)
	response[1] = b[1] + 128
	if b[1] == 0 {
		copy(response[8:], []byte{198, 51, 100, 5})
	} else {
		response = make([]byte, 16)
		response[1] = 129
		copy(response[8:10], b[4:6])
		p.r.mu.Lock()
		binary.BigEndian.PutUint16(response[10:12], p.r.external)
		lease := binary.BigEndian.Uint32(b[8:12])
		p.r.leases = append(p.r.leases, lease)
		binary.BigEndian.PutUint32(response[12:16], 120)
		if p.r.fail {
			response[3] = 2
		}
		p.r.mu.Unlock()
	}
	select {
	case p.response <- response:
	case <-p.done:
		return 0, net.ErrClosed
	}
	return len(b), nil
}
func (p *fixturePacket) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case v := <-p.response:
		return copy(b, v), net.UDPAddrFromAddrPort(ap("192.0.2.1:5351")), nil
	case <-p.done:
		return 0, nil, net.ErrClosed
	}
}
func (p *fixturePacket) Close() error                     { p.once.Do(func() { close(p.done) }); return nil }
func (p *fixturePacket) LocalAddr() net.Addr              { return net.UDPAddrFromAddrPort(ap("192.0.2.2:40000")) }
func (p *fixturePacket) SetDeadline(time.Time) error      { return nil }
func (p *fixturePacket) SetReadDeadline(time.Time) error  { return nil }
func (p *fixturePacket) SetWriteDeadline(time.Time) error { return nil }

func TestPMPGrantedLeaseConflictDeletionAndAddressChange(t *testing.T) {
	r := &pmpFixture{external: 45000}
	ctx := context.Background()
	m, e := mapPMP(ctx, r, netip.MustParseAddr("192.0.2.1"), 40000)
	if e != nil {
		t.Fatal(e)
	}
	if m.Snapshot.Address != "198.51.100.5:45000" || m.Snapshot.LeaseSeconds != 120 {
		t.Fatal(m.Snapshot)
	}
	r.external = 45001
	a, _, e := m.update(ctx, 300)
	if e != nil || a.Port() != 45001 {
		t.Fatalf("%v %v", a, e)
	}
	stopCtx, cancel := context.WithCancel(ctx)
	cancel()
	m.Maintain(stopCtx, func() { t.Error("unexpected renewal") })
	if r.leases[len(r.leases)-1] != 0 {
		t.Fatal("delete not attempted", r.leases)
	}
	r.fail = true
	if _, e = mapPMP(ctx, r, netip.MustParseAddr("192.0.2.1"), 40000); e == nil {
		t.Fatal("conflict ignored")
	}
}

func TestMappingRetainsOldLeaseUntilTransportRetires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed, deleted, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m := &Mapping{Snapshot: MappingSnapshot{Address: "198.51.100.1:40000", LeaseSeconds: 1}, update: func(_ context.Context, seconds uint32) (netip.AddrPort, uint32, error) {
		if seconds == 0 {
			close(deleted)
			return netip.AddrPort{}, 0, nil
		}
		return ap("198.51.100.1:40001"), 1, nil
	}}
	go func() { defer close(done); m.Maintain(ctx, func() { close(changed) }) }()
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("address change was not reported")
	}
	select {
	case <-deleted:
		t.Fatal("active mapping deleted before path retirement")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("mapping cleanup stalled")
	}
	select {
	case <-deleted:
	default:
		t.Fatal("retired mapping was not deleted")
	}
}
