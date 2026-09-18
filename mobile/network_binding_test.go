package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hole/core"
)

type bindingFixture struct {
	mu          sync.Mutex
	handle      string
	bindHandles []string
	lookups     []string
	failure     bool
	release     chan struct{}
	entered     chan struct{}
	once        sync.Once
}

func (f *bindingFixture) InterfacesJSON() (string, error) {
	panic("bound path must use selected Network snapshot")
}
func (f *bindingFixture) NetworkJSON() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := json.Marshal(core.NetworkSnapshot{Handle: f.handle, Interface: "wlan0", Transport: "Wi-Fi", Available: f.handle != "0", Addresses: []string{"2001:db8::1"}})
	return string(data), nil
}
func (f *bindingFixture) BindSocket(fd int64, handle string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if handle != f.handle || f.failure {
		return errors.New("stale network")
	}
	if _, err := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_TYPE); err != nil {
		return err
	}
	f.bindHandles = append(f.bindHandles, handle)
	return nil
}
func (f *bindingFixture) LookupIP(host, handle string) (string, error) {
	f.mu.Lock()
	if handle != f.handle {
		f.mu.Unlock()
		return "", errors.New("stale DNS network")
	}
	f.lookups = append(f.lookups, handle+"/"+host)
	f.mu.Unlock()
	if f.entered != nil {
		f.once.Do(func() { close(f.entered) })
	}
	if f.release != nil {
		<-f.release
	}
	return `["127.0.0.1"]`, nil
}
func fixturePlatform(f *bindingFixture) *networkPlatform {
	return &networkPlatform{provider: f, binding: f, bindingEnabled: true, lookupSlots: make(chan struct{}, 4)}
}
func TestBoundSignalDNSAndBorrowedSocketSharePinnedNetwork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = conn.Write([]byte("ok"))
		}
	}()
	f := &bindingFixture{handle: "4294967397"}
	p := fixturePlatform(f)
	defer p.close()
	selected, network, err := p.SnapshotNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if network.Handle != f.handle {
		t.Fatal("handle lost precision")
	}
	address := net.JoinHostPort("fixture.invalid", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	conn, err := selected.DialSignal(context.Background(), "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var buf [2]byte
	if _, err := conn.Read(buf[:]); err != nil || string(buf[:]) != "ok" {
		t.Fatal("borrowed fd was closed", err)
	}
	if len(f.bindHandles) != 1 || len(f.lookups) != 1 || f.bindHandles[0] != network.Handle {
		t.Fatalf("binding = %+v / %+v", f.bindHandles, f.lookups)
	}
	candidates, err := selected.Candidates(context.Background(), core.Config{}, 55140)
	if err != nil || len(candidates) != 1 || candidates[0].IP != "2001:db8::1" {
		t.Fatal("selected candidates not pinned", candidates, err)
	}
}
func TestStaleNetworkAndClosedPlatformNeverUseDefaultRoute(t *testing.T) {
	f := &bindingFixture{handle: "1"}
	p := fixturePlatform(f)
	selected, _, err := p.SnapshotNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.handle = "2"
	f.mu.Unlock()
	if _, err := selected.DialSignal(context.Background(), "tcp", "fixture.invalid:9"); err == nil {
		t.Fatal("stale handle fell back")
	}
	p.close()
	if _, err := p.DialTCP(context.Background(), "fixture.invalid:9"); !errors.Is(err, core.ErrClosed) {
		t.Fatal("closed adapter fell back", err)
	}
}
func TestBoundDNSCancellationDoesNotWaitForPlatformResolver(t *testing.T) {
	f := &bindingFixture{handle: "1", entered: make(chan struct{}), release: make(chan struct{})}
	p := fixturePlatform(f)
	defer p.close()
	selected, _, _ := p.SnapshotNetwork(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := selected.ResolveUDP(ctx, "fixture.invalid:53"); done <- err }()
	<-f.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("DNS cancellation blocked")
	}
	close(f.release)
}
func TestLoopbackUpstreamBypassesExternalBinding(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	f := &bindingFixture{handle: "0", failure: true}
	p := fixturePlatform(f)
	defer p.close()
	conn, err := p.DialTCP(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if len(f.bindHandles) != 0 || len(f.lookups) != 0 {
		t.Fatal("local application socket used external network")
	}
}
func TestNetworkEventSequencesIgnoreDelayedCallbacks(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	for _, event := range []string{`{"sequence":"9","network_handle":"new"}`, `{"sequence":"8","network_handle":"old"}`, `{"sequence":"9","network_handle":"new"}`} {
		if err := e.NetworkChanged(event); err != nil {
			t.Fatal(err)
		}
	}
	if e.networkSequence != 9 {
		t.Fatal("late callback moved sequence backward")
	}
	if e.NetworkChanged(`{"sequence":"0"}`) == nil {
		t.Fatal("zero sequence accepted")
	}
}

func TestCLIExchangeOmitsCredentialsAndRoundTripsFormats(t *testing.T) {
	input := `{"server_url":"wss://fixture.invalid/ws","room":"fixture","password":"PRIVATE_PASS","token":"PRIVATE_TOKEN","device_name":"phone","session_timeout":"1.5s","provide":[{"id":"ssh","service":"tcp://[::1]:22"}],"consume":[]}`
	redacted, err := EncodeCLIConfig(input, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE_", "password:", "token:"} {
		if strings.Contains(redacted, secret) {
			t.Fatal("credential in default YAML", redacted)
		}
	}
	decoded, err := DecodeCLIConfig(redacted)
	if err != nil {
		t.Fatal(err)
	}
	var result exchangeConfig
	if json.Unmarshal([]byte(decoded), &result) != nil {
		t.Fatal("invalid JSON")
	}
	if result.ServerURL != "wss://fixture.invalid/ws" || result.Provide[0].Service != "tcp://[::1]:22" || result.Consume == nil || result.SessionTimeout != "1.5s" {
		t.Fatal(result)
	}
	full, err := EncodeCLIConfig(input, true)
	if err != nil || !strings.Contains(full, "PRIVATE_TOKEN") {
		t.Fatal("explicit secret export failed", err)
	}
	for _, bad := range []string{input + "\n---\nroom: extra", strings.Replace(input, `"consume":[]`, `"consume":[],"enabled":true`, 1), strings.Repeat("x", 128*1024+1)} {
		if _, err := DecodeCLIConfig(bad); err == nil {
			t.Fatal("bad config accepted")
		}
	}
}
