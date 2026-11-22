package main

import (
	"strings"
	"testing"
	"time"

	"hole/core"
)

const cliFixtureConfig = `room: fixture
token: PRIVATE_ROOM_TOKEN
password: PRIVATE_SIGNAL_PASSWORD
device_name: desktop
provide: []
consume: []
`

func TestCLIRequiresServerInConfig(t *testing.T) {
	for _, value := range []string{"", "server_url: \"\"\n", "server_url: \"  \"\n"} {
		_, err := cliRequest([]byte(value+cliFixtureConfig), cliOptions{})
		if err == nil || !strings.Contains(err.Error(), "server_url") {
			t.Fatalf("missing URL accepted: %q: %v", value, err)
		}
	}
	for _, value := range []string{"https://fixture.invalid/ws", "ws://fixture.invalid/ws", "wss://user:secret@fixture.invalid/ws", "wss://fixture.invalid/ws#fragment", "not-a-url"} {
		if _, err := cliRequest([]byte("server_url: "+value+"\n"+cliFixtureConfig), cliOptions{}); err == nil {
			t.Fatalf("invalid URL accepted: %q", value)
		}
	}
}

func TestCLIConfiguredServerAndOptionalTURN(t *testing.T) {
	r, err := cliRequest([]byte("server_url: wss://fixture.invalid/custom/ws\n"+cliFixtureConfig), cliOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ServerURL != "wss://fixture.invalid/custom/ws" || r.Config.ServerURL != "" {
		t.Fatal("signaling endpoint did not move to the engine request", r.ServerURL)
	}
	c := r.Config
	if c.TURN.Mode != "worker" || c.TURN.TTL.Duration() != 6*time.Hour || c.TURN.URLs == nil || len(c.TURN.URLs) != 0 || c.TURN.Username != "" || c.TURN.Credential != "" {
		t.Fatal("unexpected default TURN configuration")
	}
	if len(c.ICE.STUNURLs) != 1 || c.ICE.STUNURLs[0] != "stun:stun.cloudflare.com:3478" {
		t.Fatal(c.ICE.STUNURLs)
	}
}

func TestCLIExplicitLocalSignal(t *testing.T) {
	if _, err := cliRequest([]byte("server_url: ws://127.0.0.1/ws\n"+cliFixtureConfig), cliOptions{allowLocalWS: true}); err != nil {
		t.Fatal("explicit local WS rejected", err)
	}
}

func TestCLIRejectsRemovedPreferred(t *testing.T) {
	for _, name := range []string{"auto", "ice", core.ProfileICE, core.ProfileFixed, "legacy-ipv6-quic-v2", "legacy", "ipv6"} {
		data := "server_url: wss://fixture.invalid/ws\n" + cliFixtureConfig + "transport:\n  preferred: " + name + "\n"
		if _, err := cliRequest([]byte(data), cliOptions{}); err == nil || !strings.Contains(err.Error(), "preferred") {
			t.Fatalf("%q: %v", name, err)
		}
	}
}

func TestCLIFixedEndpointWithoutStrategy(t *testing.T) {
	data := "server_url: wss://fixture.invalid/ws\n" + cliFixtureConfig + "transport:\n  fixed_endpoint:\n    listen: 0.0.0.0:40000\n    advertise: gateway.example:50000\n"
	r, err := cliRequest([]byte(data), cliOptions{})
	if err != nil || r.Config.Transport.FixedEndpoint == nil || r.Config.Transport.FixedEndpoint.Advertise != "gateway.example:50000" {
		t.Fatal(r, err)
	}
	if _, err := cliRequest([]byte(data+"ice:\n  relay_only: true\n"), cliOptions{}); err == nil {
		t.Fatal("CLI accepted fixed endpoint with relay_only")
	}
}
