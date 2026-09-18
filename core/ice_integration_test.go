package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func actualWorkerFixture(t *testing.T) (string, func()) {
	t.Helper()
	path, _ := filepath.Abs("../worker_fixture.test.mjs")
	command := exec.Command("node", path)
	in, e := command.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := command.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	var writeMu, socketMu sync.Mutex
	var next atomic.Int32
	sockets := map[int]*websocket.Conn{}
	send := func(value any) { writeMu.Lock(); defer writeMu.Unlock(); _ = json.NewEncoder(in).Encode(value) }
	scannerDone := make(chan struct{})
	go func() {
		defer close(scannerDone)
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 4096), 256*1024)
		for scan.Scan() {
			var m struct {
				ID    int    `json:"id"`
				Data  string `json:"data"`
				Close *struct {
					Code   int    `json:"code"`
					Reason string `json:"reason"`
				} `json:"close"`
			}
			if json.Unmarshal(scan.Bytes(), &m) != nil {
				continue
			}
			socketMu.Lock()
			ws := sockets[m.ID]
			if ws != nil {
				if m.Close != nil {
					_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(m.Close.Code, m.Close.Reason), time.Now().Add(time.Second))
					ws.Close()
				} else {
					_ = ws.WriteMessage(websocket.TextMessage, []byte(m.Data))
				}
			}
			socketMu.Unlock()
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer PRIVATE_PASSWORD" {
			http.Error(w, "bad credentials", 401)
			return
		}
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		id := int(next.Add(1))
		socketMu.Lock()
		sockets[id] = ws
		socketMu.Unlock()
		send(map[string]any{"kind": "open", "id": id, "room": r.URL.Query().Get("room")})
		defer func() {
			socketMu.Lock()
			delete(sockets, id)
			socketMu.Unlock()
			ws.Close()
			send(map[string]any{"kind": "close", "id": id})
		}()
		for {
			_, data, e := ws.ReadMessage()
			if e != nil {
				return
			}
			send(map[string]any{"kind": "message", "id": id, "data": string(data)})
		}
	}))
	t.Cleanup(func() {
		server.Close()
		in.Close()
		_ = command.Wait()
		<-scannerDone
		if stderr.Len() != 0 {
			t.Log(stderr.String())
		}
	})
	return "ws" + strings.TrimPrefix(server.URL, "http"), func() { send(map[string]any{"kind": "hibernate"}) }
}
func iceFixtureRequest(server, name string) Request {
	r := engineRequest()
	r.ServerURL = server
	r.Config.DeviceName = name
	r.Config.Provide = nil
	r.Config.Consume = nil
	r.Config.Transport = TransportConfig{Preferred: PreferredICE, AllowInsecureSignal: true}
	r.Config.ICE = ICEConfig{STUNURLs: []string{}, InterfaceAllowlist: []string{"lo"}, IncludeLoopback: true, DirectProbeTimeout: ConfigDuration(5 * time.Second)}
	r.Config.TURN.Mode = "off"
	return r
}
func TestICEMuxActualWorkerMultipleMappingsAndRecovery(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	echo, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	var accepted atomic.Int32
	go func() {
		for {
			conn, e := echo.Accept()
			if e != nil {
				return
			}
			accepted.Add(1)
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	udp, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	go func() {
		b := make([]byte, 65535)
		for {
			n, a, e := udp.ReadFrom(b)
			if e != nil {
				return
			}
			_, _ = udp.WriteTo(b[:n], a)
		}
	}()
	a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	target := ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: echo.Addr().(*net.TCPAddr).Port}
	ra.Config.Provide = []Provide{{ID: "one", Service: target}, {ID: "two", Service: target}}
	ua, ub := unusedUDPPort(t), unusedUDPPort(t)
	ta, tb := unusedTCPPort(t), unusedTCPPort(t)
	ra.Config.Consume = []Consume{{ID: "udp-a", Expose: HostPort{"127.0.0.1", ua}}, {ID: "udp-b", Expose: HostPort{"127.0.0.1", ub}}}
	rb.Config.Provide = []Provide{{ID: "udp-a", Service: ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}}, {ID: "udp-b", Service: ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "one", Expose: HostPort{"127.0.0.1", ta}}, {ID: "two", Expose: HostPort{"127.0.0.1", tb}}}
	if e = a.Start(ra); e != nil {
		t.Fatal(e)
	}
	if e = b.Start(rb); e != nil {
		t.Fatal(e)
	}
	ready := func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 4
	}
	waitSnapshot(t, a, ready)
	waitSnapshot(t, b, ready)
	dial := func(port int) net.Conn {
		t.Helper()
		c, e := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	first, second := dial(ta), dial(tb)
	defer first.Close()
	defer second.Close()
	transfer := func(c net.Conn, label string) {
		t.Helper()
		data := bytes.Repeat([]byte(label), 12000)
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		go func() { _, _ = c.Write(data) }()
		got := make([]byte, len(data))
		if _, e := io.ReadFull(c, got); e != nil {
			t.Fatalf("%s: %v / A=%+v B=%+v", label, e, a.Snapshot(), b.Snapshot())
		}
		if !bytes.Equal(data, got) {
			t.Fatal("replay changed application bytes")
		}
	}
	transfer(first, "one")
	transfer(second, "two")

	udpClients := map[int]net.Conn{}
	defer func() {
		for _, client := range udpClients {
			client.Close()
		}
	}()
	udpTransfer := func(port int, label string) {
		t.Helper()
		c := udpClients[port]
		if c == nil {
			var e error
			c, e = net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
			if e != nil {
				t.Fatal(e)
			}
			udpClients[port] = c
		}
		data := bytes.Repeat([]byte(label), 3000)
		until := time.Now().Add(6 * time.Second)
		for time.Now().Before(until) {
			_ = c.SetDeadline(time.Now().Add(300 * time.Millisecond))
			_, _ = c.Write(data)
			got := make([]byte, 65535)
			n, e := c.Read(got)
			if e == nil && bytes.Equal(data, got[:n]) {
				return
			}
		}
		t.Fatalf("UDP %s did not recover: A=%+v B=%+v", label, a.Snapshot(), b.Snapshot())
	}
	udpTransfer(ua, "udp-a")
	udpTransfer(ub, "udp-b")
	before := a.Snapshot().PeerTransports[0].Generation
	rb.Config.Consume = rb.Config.Consume[1:]
	if e = b.ApplyConfig(rb); e != nil {
		t.Fatal(e)
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		return s.SignalState == "joined" && len(s.PeerTransports) == 1 && s.PeerTransports[0].ActiveChannels == 3
	})
	if a.Snapshot().PeerTransports[0].Generation != before {
		t.Fatal("removing one mapping rebuilt the shared transport")
	}
	transfer(second, "kept")
	hibernate()
	a.mu.Lock()
	ca := a.run.ice
	a.mu.Unlock()
	_ = ca.send(signalMessage("transport_sync"))
	transfer(second, "hibernation")
	if e = a.NetworkChanged(); e != nil {
		t.Fatal(e)
	}
	waitSnapshot(t, a, func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].Generation > before
	})
	transfer(second, "network")
	udpTransfer(ua, "restored")
	if accepted.Load() != 2 {
		t.Fatalf("application sockets were replaced: %d", accepted.Load())
	}
	rb.Config.Provide = nil
	rb.Config.Consume = nil
	if e = b.ApplyConfig(rb); e != nil {
		t.Fatal(e)
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		return len(s.PeerTransports) == 0 && s.SignalState == "joined"
	})
	time.Sleep(50 * time.Millisecond)
	if b.Snapshot().SignalState != "joined" {
		t.Fatal("closed transport error corrupted signaling state")
	}
}
