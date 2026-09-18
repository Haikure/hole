package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"hole/core"
)

// Exercise the actual Engine against local WebSocket signaling, not a mocked
// lifecycle implementation. No public STUN/TURN server or device is contacted.
func TestBridgeRealCoreStartReconfigureNetworkStopRestart(t *testing.T) {
	var joined, active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer PRIVATE_PASSWORD" || r.URL.Query().Get("room") != "ROOM" {
			http.Error(w, "invalid fixture credentials", http.StatusUnauthorized)
			return
		}
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var join struct {
			Type string `json:"type"`
		}
		if ws.ReadJSON(&join) != nil || join.Type != "join" {
			return
		}
		active.Add(1)
		defer active.Add(-1)
		joined.Add(1)
		if ws.WriteJSON(map[string]any{"type": "joined", "signal_version": 2}) != nil {
			return
		}
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client, hostPipe := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, hostPipe, hostPipe) }()
	decoder := json.NewDecoder(client)
	nextID := 0
	var eventCount int
	call := func(method, params string) wireMessage {
		t.Helper()
		nextID++
		id := strconv.Itoa(nextID)
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := client.Write([]byte(wireRequest(id, method, params))); err != nil {
			t.Fatal(err)
		}
		for {
			var m wireMessage
			if err := decoder.Decode(&m); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(m)
			if bytes.Contains(data, []byte("PRIVATE_")) {
				t.Fatal("credentials appeared in stdout")
			}
			if m.Method == "event" {
				eventCount++
				continue
			}
			if m.ID == nil || *m.ID != id || m.Error != nil {
				t.Fatalf("%s reply: %+v", method, m)
			}
			return m
		}
	}
	waitState := func(predicate func(core.Snapshot) bool) core.Snapshot {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for {
			m := call("snapshot", "")
			var result snapshotResult
			if err := json.Unmarshal(m.Result, &result); err != nil {
				t.Fatal(err)
			}
			if predicate(result.Snapshot) {
				return result.Snapshot
			}
			if time.Now().After(until) {
				t.Fatalf("state not reached: %+v", result)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	params := strings.Replace(validParams(), "wss://example.invalid/ws", "ws"+strings.TrimPrefix(server.URL, "http"), 1)
	params = strings.Replace(params, `"preferred":"ice"`, `"preferred":"ice","allow_insecure_signal":true`, 1)
	call("hello", "")
	call("validate", params)
	if joined.Load() != 0 {
		t.Fatal("hello/validate opened a signaling connection")
	}
	call("start", params)
	first := waitState(func(s core.Snapshot) bool { return s.SignalState == "joined" })
	updated := strings.Replace(params, `"device_name":"desktop"`, `"device_name":"desktop-2"`, 1)
	call("apply_config", updated)
	waitState(func(s core.Snapshot) bool { return s.SignalState == "joined" && s.Generation == first.Generation })
	call("network_changed", "")
	waitState(func(s core.Snapshot) bool {
		return s.SignalState == "joined" && s.NetworkChanges > 0 && joined.Load() >= 2
	})
	call("stop", "")
	waitState(func(s core.Snapshot) bool { return s.EngineState == "stopped" && !s.RunRequested })
	call("start", updated)
	waitState(func(s core.Snapshot) bool { return s.SignalState == "joined" && s.Generation > first.Generation })
	call("shutdown", "")
	if err := waitServe(t, done); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 || eventCount == 0 {
		t.Fatalf("shutdown/notifications: active=%d events=%d", active.Load(), eventCount)
	}
}
