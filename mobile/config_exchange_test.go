package mobile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
}

func TestCLIExchangeValidatesConfiguredServer(t *testing.T) {
	for _, server := range []string{"https://fixture.invalid/ws", "wss://PRIVATE_USER:PRIVATE_PASSWORD@fixture.invalid/ws", "wss://fixture.invalid/ws#fragment"} {
		_, err := DecodeCLIConfig("server_url: " + server + "\nprovide: []\nconsume: []\n")
		if err == nil || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatal("bad server accepted or exposed in an error", err)
		}
	}
}

func TestCLIExchangeRejectsOldPreferredAndExportsShortNames(t *testing.T) {
	for _, name := range []string{"ice-quic-mux-v1", "legacy-ipv6-quic-v2", "legacy", "auto"} {
		if _, err := DecodeCLIConfig("transport:\n  preferred: " + name + "\n"); err == nil {
			t.Fatal("old preferred accepted", name)
		}
	}
	for _, name := range []string{"ice", "ipv6"} {
		text, err := EncodeCLIConfig(`{"transport":{"preferred":"`+name+`"},"provide":[],"consume":[]}`, false)
		if err != nil || !strings.Contains(text, "preferred: "+name) {
			t.Fatal(name, text, err)
		}
	}
}
