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
transport:
  preferred: ice
  allow_legacy: true
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
	if c.Transport.Preferred != core.PreferredICE || !c.Transport.AllowLegacy {
		t.Fatal(c.Transport)
	}
}

func TestCLIModeOverridesAndLocalSignal(t *testing.T) {
	for _, tc := range []struct {
		mode, profile string
		legacy        bool
	}{
		{"", core.PreferredICE, true}, {"auto", core.PreferredICE, true},
		{"ice", core.PreferredICE, false}, {"ipv6", core.PreferredIPv6, false},
	} {
		r, err := cliRequest([]byte("server_url: wss://fixture.invalid/ws\n"+cliFixtureConfig), cliOptions{transport: tc.mode})
		if err != nil || r.Config.Transport.Preferred != tc.profile || r.Config.Transport.AllowLegacy != tc.legacy {
			t.Fatal(tc, r.Config.Transport, err)
		}
	}
	for _, options := range []cliOptions{{transport: "invalid"}} {
		if _, err := cliRequest([]byte("server_url: wss://fixture.invalid/ws\n"+cliFixtureConfig), options); err == nil {
			t.Fatal("invalid options accepted", options)
		}
	}
	if _, err := cliRequest([]byte("server_url: ws://127.0.0.1/ws\n"+cliFixtureConfig), cliOptions{allowLocalWS: true}); err != nil {
		t.Fatal("explicit local WS rejected", err)
	}
}

func TestCLIRejectsOldPreferredNames(t *testing.T) {
	for _, name := range []string{core.ProfileICE, core.ProfileLegacy, "auto", "legacy"} {
		data := "server_url: wss://fixture.invalid/ws\n" + strings.Replace(cliFixtureConfig, "preferred: ice", "preferred: "+name, 1)
		if _, err := cliRequest([]byte(data), cliOptions{}); err == nil || !strings.Contains(err.Error(), "ice 或 ipv6") {
			t.Fatalf("%q: %v", name, err)
		}
	}
}
