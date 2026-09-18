package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"hole/core"
)

func validParams() string {
	return `{"api_version":1,"server_url":"wss://example.invalid/ws","config":{"room":"ROOM","password":"PRIVATE_PASSWORD","token":"PRIVATE_TOKEN","device_name":"desktop","transport":{"preferred":"ice"},"ice":{"stun_urls":[]},"turn":{"mode":"off"},"provide":[],"consume":[]}}`
}

func wireRequest(id, method, params string) string {
	frame := `{"jsonrpc":"2.0","id":"` + id + `","method":"` + method + `"`
	if params != "" {
		frame += `,"params":` + params
	}
	return frame + "}\n"
}

func TestRequestEnvelopeValidation(t *testing.T) {
	good := strings.TrimSpace(wireRequest("request-1:2", "hello", "{}"))
	for _, data := range []string{good, " \t" + good + "\r\n"} {
		r, err := decodeRequest([]byte(data))
		if err != nil || r.ID == nil || *r.ID != "request-1:2" {
			t.Fatalf("valid envelope: %+v %+v", r, err)
		}
	}
	tests := []struct {
		name string
		data string
		code int
	}{
		{"syntax", `{`, parseError},
		{"utf8", "{\"method\":\"\xff\"}", parseError},
		{"trailing document", good + `{}`, parseError},
		{"batch", `[` + good + `]`, invalidRequest},
		{"null", `null`, invalidRequest},
		{"unknown field", strings.TrimSuffix(good, "}") + `,"extra":1}`, invalidRequest},
		{"uppercase field", strings.Replace(good, `"jsonrpc"`, `"JSONRPC"`, 1), invalidRequest},
		{"wrong rpc version", strings.Replace(good, `"2.0"`, `"1.0"`, 1), invalidRequest},
		{"missing ID", `{"jsonrpc":"2.0","method":"shutdown"}`, invalidRequest},
		{"null ID", strings.Replace(good, `"request-1:2"`, `null`, 1), invalidRequest},
		{"numeric ID", strings.Replace(good, `"request-1:2"`, `9007199254740993`, 1), invalidRequest},
		{"empty ID", strings.Replace(good, `"request-1:2"`, `""`, 1), invalidRequest},
		{"oversized ID", strings.Replace(good, `request-1:2`, strings.Repeat("a", maxRequestID+1), 1), invalidRequest},
		{"control ID", strings.Replace(good, `request-1:2`, `a\nb`, 1), invalidRequest},
		{"duplicate key", strings.TrimSuffix(good, "}") + `,"method":"shutdown"}`, invalidRequest},
		{"escaped duplicate", strings.TrimSuffix(good, "}") + `,"\u0069d":"second"}`, invalidRequest},
		{"nested duplicate", wireRequest("1", "start", `{"config":{"token":"a","token":"b"}}`), invalidRequest},
		{"nested alias", wireRequest("1", "start", `{"config":{"token":"a","\u0074oken":"b"}}`), invalidRequest},
		{"depth", wireRequest("1", "hello", strings.Repeat("[", maxRequestDepth+1)+"0"+strings.Repeat("]", maxRequestDepth+1)), invalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := decodeRequest([]byte(tc.data))
			if err == nil || err.Code != tc.code || r.ID != nil {
				t.Fatalf("got %+v %+v, want code %d and null response ID", r, err, tc.code)
			}
		})
	}
}

func TestConfigurationUsesCoreParserWithoutRevisionCounter(t *testing.T) {
	r, err := engineRequest([]byte(validParams()))
	if err != nil {
		t.Fatal(err)
	}
	if r.Config.Transport.Preferred != core.PreferredICE || r.Config.TURN.Mode != "off" || r.Config.ICE.STUNURLs == nil || len(r.Config.ICE.STUNURLs) != 0 {
		t.Fatalf("configuration changed: %+v", r)
	}
	if r.Config.SessionTimeout == 0 || r.Config.ICE.GatherTimeout == 0 {
		t.Fatal("shared normalization was skipped")
	}
	legacy := strings.Replace(validParams(), `,"server_url"`, `,"config_revision":"9007199254740993","server_url"`, 1)
	if _, err := engineRequest([]byte(legacy)); err != nil {
		t.Fatal("legacy config_revision was not accepted:", err)
	}
}

func TestConfigurationFailuresAreBoundedAndDoNotEchoSecrets(t *testing.T) {
	valid := validParams()
	tests := []string{
		`null`, `[]`, `{}`, `{"api_version":2}`,
		strings.Replace(valid, `"api_version":1`, `"api_version":2`, 1),
		strings.Replace(valid, `"api_version":1`, `"API_VERSION":1`, 1),
		strings.TrimSuffix(valid, "}") + `,"extra":"PRIVATE_PAYLOAD"}`,
		strings.Replace(valid, `"config":{`, `"config":{"server_url":"wss://PRIVATE_PAYLOAD/",`, 1),
		strings.Replace(valid, `"config":{`, `"config":{"unexpected":"PRIVATE_PAYLOAD",`, 1),
		strings.Replace(valid, `"provide":[]`, `"provide":[{"id":"test","service":"PRIVATE_PAYLOAD"}]`, 1),
		strings.Replace(valid, `wss://example.invalid/ws`, `http://PRIVATE_PAYLOAD/`, 1),
		strings.Replace(valid, `wss://example.invalid/ws`, `ws://example.invalid/ws`, 1),
		strings.Replace(valid, `"device_name":"desktop"`, `"device_name":""`, 1),
		strings.Replace(valid, `"preferred":"ice"`, `"preferred":"PRIVATE_PASSWORD"`, 1),
	}
	for i, data := range tests {
		_, err := engineRequest([]byte(data))
		if err == nil || err.Code != invalidParams {
			t.Fatalf("case %d: expected invalid params, got %+v", i, err)
		}
		encoded, _ := json.Marshal(err)
		if strings.Contains(string(encoded), "PRIVATE_") {
			t.Fatalf("case %d reflected a secret: %s", i, encoded)
		}
	}
}

func TestOutputBoundAndNoParams(t *testing.T) {
	if _, err := encodeMessage(strings.Repeat("x", MaxOutputBytes)); err == nil {
		t.Fatal("oversized response accepted")
	}
	for _, params := range []string{"", `{}`, " { } "} {
		if !noParams([]byte(params)) {
			t.Fatalf("empty params rejected: %q", params)
		}
	}
	for _, params := range []string{`null`, `[]`, `{"x":1}`, `""`, `{`} {
		if noParams([]byte(params)) {
			t.Fatalf("nonempty params accepted: %q", params)
		}
	}
}

func FuzzDecodeRequest(f *testing.F) {
	for _, seed := range []string{wireRequest("1", "hello", "{}"), wireRequest("2", "start", validParams()), `null`, `[]`, `{`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxRequestBytes {
			return
		}
		r, err := decodeRequest(data)
		if err == nil && (!validID(r.ID) || r.JSONRPC != "2.0") {
			t.Fatal("invalid envelope accepted")
		}
	})
}
