package desktop

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hole/core"
	"hole/mobile" // Contract tests only; the desktop executable does not import mobile.
)

func jsonObject(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPortableConfigurationMatchesAndroidExchange(t *testing.T) {
	fixtures := []string{
		`{}`,
		"room: ROOM\nprovide: []\nconsume: []\n",
		"server_url: wss://HOST/ws\ntransport:\n  preferred: ice\n  allow_legacy: true\nice:\n  stun_urls: []\nturn:\n  mode: off\n",
		`{"server_url":"wss://HOST/ws","room":"ROOM","password":"PRIVATE_PASSWORD","token":"PRIVATE_TOKEN","device_name":"desktop","transport":{"preferred":"ice"},"ice":{"stun_urls":[],"relay_only":true,"include_loopback":true,"interface_allowlist":["lo"],"direct_probe_timeout":"4s"},"turn":{"mode":"manual","ttl":"2h","urls":["turn:HOST:3478?transport=udp"],"username":"TURN_USER","credential":"PRIVATE_TURN"},"provide":[{"id":"tcp","service":"tcp://[::1]:22"},{"id":"udp","service":"udp://HOST:53"}],"consume":[{"id":"other","expose":"127.0.0.1:8022"}]}`,
		`{"transport":{"preferred":"ipv6"},"candidate_interfaces":["eth0"],"session_timeout":"30m","turn":{"mode":"manual","urls":["turn:HOST:3478"]}}`,
	}
	for i, text := range fixtures {
		doc, fault := portableDocument([]byte(text))
		mobileJSON, err := mobile.DecodeCLIConfig(text)
		if fault != nil || err != nil {
			t.Fatalf("fixture %d import: %+v %v", i, fault, err)
		}
		desktopJSON, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(jsonObject(t, desktopJSON), jsonObject(t, []byte(mobileJSON))) {
			t.Fatalf("fixture %d desktop/mobile import contract diverged", i)
		}
		for _, includeSecrets := range []bool{false, true} {
			params, _ := json.Marshal(map[string]any{"config": json.RawMessage(desktopJSON), "include_secrets": includeSecrets})
			result, fault := exchangeConfig("encode_cli_config", params)
			mobileYAML, err := mobile.EncodeCLIConfig(mobileJSON, includeSecrets)
			if fault != nil || err != nil {
				t.Fatalf("fixture %d export: %+v %v", i, fault, err)
			}
			encoded, _ := json.Marshal(result)
			var exported struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(encoded, &exported)
			if exported.Text != mobileYAML {
				t.Fatalf("fixture %d desktop/mobile exported YAML differs", i)
			}
			if !includeSecrets && strings.Contains(exported.Text, "PRIVATE_") {
				t.Fatal("default export included credentials")
			}
			if _, fault := portableDocument([]byte(exported.Text)); fault != nil {
				t.Fatal("export is not importable as a stopped draft")
			}
		}
	}
}

func TestConfigExchangeRPCIsReadOnlyAndCredentialsAreOptInOnExport(t *testing.T) {
	document := `{"server_url":"wss://HOST/ws","room":"ROOM","password":"PRIVATE_PASSWORD","token":"PRIVATE_TOKEN","transport":{"preferred":"ice"},"turn":{"mode":"manual","urls":["turn:HOST:3478"],"username":"USER","credential":"PRIVATE_TURN"}}`
	decode, _ := json.Marshal(map[string]any{"text": document})
	encode, _ := json.Marshal(map[string]any{"config": json.RawMessage(document)})
	withSecrets, _ := json.Marshal(map[string]any{"config": json.RawMessage(document), "include_secrets": true})
	e := core.NewEngine(core.Options{})
	ms, err := exchange(t, wireRequest("1", "decode_cli_config", string(decode))+
		wireRequest("2", "encode_cli_config", string(encode))+wireRequest("3", "encode_cli_config", string(withSecrets))+
		wireRequest("4", "snapshot", ""), e)
	if err != nil || len(ms) != 4 {
		t.Fatalf("exchange RPC: %d %v", len(ms), err)
	}
	for i, m := range ms {
		if m.Error != nil {
			t.Fatalf("RPC %d: %+v", i, m.Error)
		}
	}
	if !strings.Contains(string(ms[0].Result), "PRIVATE_PASSWORD") || strings.Contains(string(ms[1].Result), "PRIVATE_") || !strings.Contains(string(ms[2].Result), "PRIVATE_TURN") {
		t.Fatal("import/explicit export credential policy changed")
	}
	var snapshot snapshotResult
	_ = json.Unmarshal(ms[3].Result, &snapshot)
	if snapshot.Snapshot.Configured || snapshot.Snapshot.RunRequested || snapshot.Snapshot.Generation != 0 {
		t.Fatal("preview/export changed the running configuration")
	}
}

func TestImportedDocumentLimitSurvivesJSONEscaping(t *testing.T) {
	prefix := "room: ROOM\n#"
	document := prefix + strings.Repeat("\t", MaxConfigBytes-len(prefix)-1) + "\n"
	params, _ := json.Marshal(map[string]string{"text": document})
	if len(params) <= MaxConfigBytes || len(params) >= MaxRequestBytes {
		t.Fatal("fixture does not exercise envelope/document size separation")
	}
	ms, err := exchange(t, wireRequest("1", "decode_cli_config", string(params)), newFakeEngine())
	if err != nil || len(ms) != 1 || ms[0].Error != nil {
		t.Fatalf("valid 128 KiB import rejected: %+v %v", ms, err)
	}
	params, _ = json.Marshal(map[string]string{"text": document + " "})
	ms, err = exchange(t, wireRequest("1", "decode_cli_config", string(params))+wireRequest("2", "hello", ""), newFakeEngine())
	if err != nil || len(ms) != 2 || ms[0].Error == nil || ms[0].Error.Data.Code != "invalid_document" || ms[1].Error != nil {
		t.Fatalf("document limit should reject only the request: %+v %v", ms, err)
	}
	largeRun := strings.TrimSuffix(validParams(), "}") + strings.Repeat(" ", MaxConfigBytes) + "}"
	if _, fault := engineRequest([]byte(largeRun)); fault == nil || fault.Code != invalidParams {
		t.Fatal("larger IPC envelope weakened the runtime request limit")
	}
}

func TestConfigExchangeRejectsInvalidDocumentsWithoutSecretEcho(t *testing.T) {
	for _, document := range []string{
		"", "room: a\nroom: b\n", "room: ROOM\n---\nroom: another\n",
		`{"server_url":"https://PRIVATE_PASSWORD/ws"}`,
		`{"server_url":"wss://PRIVATE_USER:PRIVATE_PASSWORD@HOST/ws"}`,
		`{"transport":{"preferred":"ice-quic-mux-v1"}}`,
		`{"unexpected":"PRIVATE_PASSWORD"}`,
		`{"provide":[{"id":"p","service":"PRIVATE_PASSWORD"}]}`,
	} {
		_, fault := portableDocument([]byte(document))
		_, androidErr := mobile.DecodeCLIConfig(document)
		if fault == nil || androidErr == nil {
			t.Fatal("desktop/mobile validation accepted an invalid document")
		}
		encoded, _ := json.Marshal(fault)
		if strings.Contains(string(encoded), "PRIVATE_") {
			t.Fatal("invalid document error reflected credentials")
		}
	}
	for _, params := range []string{`null`, `{}`, `{"config":null}`, `{"config":[]}`, `{"config":{},"include_secrets":"yes"}`, `{"config":{},"unexpected":true}`} {
		if _, fault := exchangeConfig("encode_cli_config", []byte(params)); fault == nil {
			t.Fatalf("invalid export params accepted: %s", params)
		}
	}
}
