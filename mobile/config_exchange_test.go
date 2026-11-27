package mobile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHoleCLIExchangePreservesOrderPresenceAndRejectsServer(t *testing.T) {
	for _, order := range []string{"", "  order: []\n", "  order: [hole, tls]\n"} {
		source := "device_name: phone\nrelay:\n  device: relay-node\n" + order
		text, err := DecodeCLIConfig(source)
		if err != nil {
			t.Fatal(err)
		}
		var before exchangeConfig
		if err = json.Unmarshal([]byte(text), &before); err != nil {
			t.Fatal(err)
		}
		if (before.Relay.Order == nil) != (order == "") {
			t.Fatal("lost order presence", text)
		}
		yamlText, err := EncodeCLIConfig(text, false)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeCLIConfig(yamlText)
		if err != nil || back != text {
			t.Fatal("relay round trip", err, back, text)
		}
	}
	if _, err := DecodeCLIConfig("transport:\n  fixed_endpoint:\n    listen: 0.0.0.0:40000\n    advertise: relay.example:40000\nrelay:\n  serve:\n    enabled: true\n"); err == nil || !strings.Contains(err.Error(), "仅 CLI 支持") {
		t.Fatal(err)
	}
}

func TestHoleGUIDefaultServerSettings(t *testing.T) {
	if _, err := DecodeCLIConfig(`{"transport":{},"relay":{"device":"relay-node","order":null,"serve":{"enabled":false,"max_pairs":64,"bandwidth_mbps":0}}}`); err != nil {
		t.Fatal("GUI default relay settings rejected", err)
	}
}

func TestCLIExchangePreservesExplicitDiscovery(t *testing.T) {
	for _, discovery := range []string{"", "    discovery: minecraft\n", "    discovery: minecraft\n    minecraft: {description: 海岛, version: 1.20.1, online: 2, max: 20}\n"} {
		text, err := DecodeCLIConfig("provide:\n  - id: world\n    service: tcp://127.0.0.1:25565\n" + discovery)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, `"discovery":"minecraft"`) != (discovery != "") {
			t.Fatal(text)
		}
		if strings.Contains(discovery, "description:") && !strings.Contains(text, `"online":2`) {
			t.Fatal("lost Minecraft metadata", text)
		}
		exported, err := EncodeCLIConfig(text, false)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeCLIConfig(exported)
		if err != nil || back != text {
			t.Fatal("discovery round trip", err, back, text)
		}
	}
}

func TestOldCLIImportRetainsEmptyServerAndWorkerDefaults(t *testing.T) {
	text, err := DecodeCLIConfig("room: fixture\nprovide: []\nconsume: []\n")
	if err != nil {
		t.Fatal(err)
	}
	var doc exchangeConfig
	if err = json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ServerURL != "" || doc.TURN.Mode != "worker" || doc.TURN.TTL.Duration() != 6*time.Hour || doc.TURN.URLs == nil {
		t.Fatal("old draft import changed defaults")
	}
	if doc.ICE.InterfaceAllowlist == nil {
		t.Fatal("CLI JSON must encode the GUI interface allowlist as an array")
	}
}

func TestCLIExchangeValidatesConfiguredServer(t *testing.T) {
	for _, server := range []string{"https://fixture.invalid/ws", "wss://PRIVATE_USER:PRIVATE_PASSWORD@fixture.invalid/ws", "wss://fixture.invalid/ws#fragment"} {
		_, err := DecodeCLIConfig("server_url: " + server + "\nprovide: []\nconsume: []\n")
		if err == nil || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatal("bad server accepted or exposed in an error", err)
		}
	}
}

func TestCLIExchangeRejectsRemovedPreferred(t *testing.T) {
	for _, name := range []string{"auto", "ice", "ice-quic-mux-v1", "legacy-ipv6-quic-v2", "fixed-quic-mux-v1", "legacy", "ipv6"} {
		if _, err := DecodeCLIConfig("transport:\n  preferred: " + name + "\n"); err == nil {
			t.Fatal("removed preferred accepted on import", name)
		}
		if _, err := EncodeCLIConfig(`{"transport":{"preferred":"`+name+`"},"provide":[],"consume":[]}`, false); err == nil {
			t.Fatal("removed preferred accepted on export", name)
		}
	}
	text, err := DecodeCLIConfig("provide: []\nconsume: []\n")
	var doc exchangeConfig
	if err != nil || json.Unmarshal([]byte(text), &doc) != nil || strings.Contains(text, "preferred") {
		t.Fatal("default import retained strategy field", text, err)
	}
	text, err = EncodeCLIConfig(text, false)
	if err != nil || strings.Contains(text, "preferred") {
		t.Fatal("default export retained strategy field", text, err)
	}
}
