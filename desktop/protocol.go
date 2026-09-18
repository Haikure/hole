// Package desktop hosts the shared core over a bounded, versioned stdio
// protocol. It is not imported by the CLI, mobile facade, or shared core.
package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"hole/core"
)

const (
	ProtocolVersion = 1
	// A 128 KiB imported document can expand up to sixfold when JSON-escaped.
	// Keep a separate document/run-request limit rather than shrinking imports.
	MaxConfigBytes  = 128 * 1024
	MaxRequestBytes = 1024 * 1024
	MaxOutputBytes  = 2 * 1024 * 1024
	maxRequestDepth = 64
	maxRequestID    = 64

	parseError     = -32700
	invalidRequest = -32600
	methodNotFound = -32601
	invalidParams  = -32602
	internalError  = -32603
	coreError      = -32000
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *string         `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    *core.Fault `json:"data,omitempty"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      *string   `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string     `json:"jsonrpc"`
	Method  string     `json:"method"`
	Params  core.Event `json:"params"`
}

type snapshotResult struct {
	Snapshot            core.Snapshot `json:"snapshot"`
	BridgeEventsDropped uint64        `json:"bridge_events_dropped,string"`
}

type protocolLimits struct {
	RequestBytes  int `json:"request_bytes"`
	ConfigBytes   int `json:"config_bytes"`
	OutputBytes   int `json:"output_bytes"`
	QueuedEvents  int `json:"queued_events"`
	QueuedReplies int `json:"queued_responses"`
	WriteMS       int `json:"write_timeout_ms"`
}

type helloResult struct {
	BridgeVersion int            `json:"bridge_version"`
	APIVersion    int            `json:"api_version"`
	CoreVersion   string         `json:"core_version"`
	Methods       []string       `json:"methods"`
	Limits        protocolLimits `json:"limits"`
}

func rpcFault(code int, kind, message string) *rpcError {
	return &rpcError{Code: code, Message: message, Data: &core.Fault{Code: kind, Message: message}}
}

func operationFault(err error) *rpcError {
	var fault *core.Fault
	if errors.As(err, &fault) {
		copy := *fault // Core already redacts configuration secrets in Faults.
		return &rpcError{Code: coreError, Message: "核心操作未完成", Data: &copy}
	}
	// Do not reflect arbitrary errors or request bodies into the protocol/logs.
	return rpcFault(coreError, "core_error", "核心操作未完成")
}

func decodeRequest(frame []byte) (request, *rpcError) {
	if !utf8.Valid(frame) || !json.Valid(frame) {
		return request{}, rpcFault(parseError, "invalid_json", "请求需要有效的 UTF-8 JSON")
	}
	frame = bytes.TrimSpace(frame)
	if len(frame) == 0 || frame[0] != '{' || !unambiguousJSON(frame) {
		return request{}, rpcFault(invalidRequest, "invalid_request", "请求需要单个对象、唯一字段和至多 64 层嵌套")
	}
	var r request
	if !onlyFields(frame, "jsonrpc", "id", "method", "params") || decodeStrict(frame, &r) != nil {
		return request{}, rpcFault(invalidRequest, "invalid_request", "请求字段或类型无效")
	}
	if r.JSONRPC != "2.0" || !validID(r.ID) || r.Method == "" || len(r.Method) > 64 {
		return request{}, rpcFault(invalidRequest, "invalid_request", "需要 jsonrpc 2.0、方法名和 1 到 64 字节的字符串 ID")
	}
	return r, nil
}

func validID(id *string) bool {
	if id == nil || len(*id) == 0 || len(*id) > maxRequestID {
		return false
	}
	for _, c := range *id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}

func decodeStrict(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// Match field names exactly; encoding/json's case-insensitive fallback is not
// part of the wire protocol and could otherwise alias two distinct JSON keys.
func onlyFields(data []byte, allowed ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	for key := range fields {
		known := false
		for _, name := range allowed {
			known = known || key == name
		}
		if !known {
			return false
		}
	}
	return true
}

// encoding/json normally accepts duplicate keys and keeps the last value.
// Reject ambiguity in both the envelope and nested configuration before any
// lifecycle operation. The depth limit also bounds recursive validation work.
func unambiguousJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > maxRequestDepth {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, container := token.(json.Delim)
		if !container {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] || !value(depth+1) {
					return false
				}
				seen[name] = true
			}
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, err = d.Token() // json.Valid has already checked matching delimiters.
		return err == nil
	}
	return value(1)
}

func noParams(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var fields map[string]json.RawMessage
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '{' && json.Unmarshal(raw, &fields) == nil && len(fields) == 0
}

func engineRequest(raw json.RawMessage) (core.Request, *rpcError) {
	var wire struct {
		APIVersion int    `json:"api_version"`
		ServerURL  string `json:"server_url"`
		// Deprecated input accepted for compatibility; ordering is serialized
		// by the Engine API and does not use a caller-maintained counter.
		LegacyRevision json.RawMessage `json:"config_revision"`
		Config         json.RawMessage `json:"config"`
	}
	bad := func(message string) (core.Request, *rpcError) {
		return core.Request{}, rpcFault(invalidParams, "invalid_config", message)
	}
	if len(raw) > MaxConfigBytes {
		return bad("完整运行请求超过 128 KiB")
	}
	if len(raw) == 0 || !onlyFields(raw, "api_version", "server_url", "config_revision", "config") || decodeStrict(raw, &wire) != nil {
		return bad("params 需要 api_version、server_url 和 config")
	}
	if wire.APIVersion != core.APIVersion {
		return core.Request{}, rpcFault(invalidParams, "api_version_mismatch", "核心 API 版本不匹配")
	}
	configJSON := bytes.TrimSpace(wire.Config)
	if len(configJSON) == 0 || configJSON[0] != '{' {
		return bad("config 需要完整的配置对象")
	}
	// Share configuration parsing, defaults and validation with every client;
	// only the desktop envelope is implemented here.
	config, err := core.ParseConfig(configJSON)
	if err != nil {
		return bad("配置格式无效，请检查字段、协议、地址与时长")
	}
	if config.ServerURL != "" {
		return bad("server_url 只填写在 params 中，config 中不重复填写")
	}
	r := core.Request{ServerURL: wire.ServerURL, Config: config}
	if err = r.Validate(); err != nil {
		fault := operationFault(err)
		fault.Code = invalidParams
		fault.Message = "配置校验未通过"
		return core.Request{}, fault
	}
	return r, nil
}

func encodeMessage(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxOutputBytes {
		return nil, errors.New("output frame limit")
	}
	return append(data, '\n'), nil
}
