package core

import (
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSignalDisconnectCausePrecedesDelegationOrImmediateRecovery(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		name := "last_signal_connection"
		if delegated {
			name = "delegated"
		}
		t.Run(name, func(t *testing.T) {
			c, peer := roomTestCoordinator(t, "a"), roomTestCoordinator(t, "b")
			c.online, c.sink = false, nil
			c.room.forceSignal = true
			if delegated {
				c.room.directory["b"] = voiceRoomMember{DeviceName: "b", RuntimeID: peer.runtimeID, CoreVersion: CoreVersion,
					CertFingerprint: peer.fingerprint, TransportEpoch: 1}
				c.room.nodes["b"] = roomNodeRecord{node: roomNode{DeviceName: "b", RuntimeID: peer.runtimeID, CoreVersion: CoreVersion,
					WorkerVersion: "1.1.0", Epoch: 1, Revision: 1, SignalOnline: true}}
				c.room.peers["ab"] = &roomTestPeer{link: roomLink{PeerID: "b", RuntimeID: peer.runtimeID, TransportID: "ab", Generation: 1}}
			}
			key, err := x509.MarshalPKIXPublicKey(c.room.workerKey)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var join iceSignalMessage
				if conn.ReadJSON(&join) != nil {
					return
				}
				joined := signalMessage("joined")
				joined.SignalVersion, joined.SignalResume, joined.LiveConfig = 2, true, true
				joined.WorkerVersion, joined.RoomPublicKey = "1.1.0", base64.StdEncoding.EncodeToString(key)
				if conn.WriteJSON(joined) != nil || conn.WriteJSON(signalMessage("transport_synced")) != nil {
					return
				}
				// Lose an established signal session, after the client has consumed
				// the sync reply. Otherwise the socket error can win its select and
				// legitimately leave the explicit recovery request unfinished.
				for deadline := time.Now().Add(2 * time.Second); ; {
					c.room.mu.Lock()
					synced := c.room.synced
					c.room.mu.Unlock()
					if synced {
						break
					}
					if time.Now().After(deadline) {
						t.Error("client did not consume the signal sync reply")
						return
					}
					time.Sleep(time.Millisecond)
				}
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "fixture signal loss "+c.initial.Config.Token), time.Now().Add(time.Second))
			}))
			defer server.Close()
			c.initial.ServerURL = "ws" + strings.TrimPrefix(server.URL, "http")
			c.desired = c.initial
			c.platform = roomNoNetworkPlatform{}
			c.sessions = newSessionManager(c.initial.Config.SessionTimeout, sessionOptions{platform: c.platform, shared: true})
			defer c.sessions.close()
			events := make(chan Event, 128)
			c.emit = func(event Event) {
				select {
				case events <- event:
				case <-c.ctx.Done():
				}
			}
			done := make(chan error, 1)
			go func() { done <- c.run() }()
			defer func() { c.cancel(); <-done }()
			joined, failed := false, false
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			for {
				select {
				case event := <-events:
					if event.Kind != "signal" {
						continue
					}
					switch event.State {
					case "joined":
						joined = true
					case "reconnecting":
						if !joined || event.Error == nil || !strings.Contains(event.Error.Message, "fixture signal loss [redacted]") || strings.Contains(event.Error.Message, c.initial.Config.Token) {
							t.Fatalf("disconnect lost or leaked the original cause: %+v", event)
						}
						failed = true
					case "delegated":
						if !delegated || !failed {
							t.Fatal("delegation appeared before its disconnect cause")
						}
						return
					case "connecting":
						if joined {
							if delegated || !failed {
								t.Fatal("immediate recovery skipped its disconnect cause")
							}
							return
						}
					}
				case <-timer.C:
					t.Fatalf("missing disconnect and recovery events: joined=%t failed=%t", joined, failed)
				case err := <-done:
					done <- err
					t.Fatalf("coordinator ended early: %v", err)
				}
			}
		})
	}
}
