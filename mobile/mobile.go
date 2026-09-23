// Package mobile is the gomobile-compatible facade. No context, map, socket,
// duration or uint64 crosses JNI directly; requests and events are versioned JSON.
package mobile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"hole/core"
)

// EventSink runs on one Go-managed background thread, never the Android UI
// thread. Implementations must return promptly and dispatch lifecycle commands
// elsewhere, not call Start/Stop/Close synchronously from OnEvent.
type EventSink interface {
	OnEvent(eventJSON string)
}

type Engine struct {
	operations      sync.Mutex
	callbacks       sync.Mutex
	engine          *core.Engine
	sink            EventSink
	enabled         bool
	generation      uint64
	pumpDone        chan struct{}
	network         *networkPlatform
	networkSequence uint64
}

func Version() int { return core.APIVersion }

// NewEngine does not read credentials or open sockets. Close releases the sink.
func NewEngine(events EventSink) *Engine {
	return newEngine(events, nil)
}

func newEngine(events EventSink, platform core.Platform) *Engine {
	e := &Engine{engine: core.NewEngine(core.Options{Platform: platform, RetryNetwork: true}), sink: events, pumpDone: make(chan struct{})}
	go e.pump()
	return e
}

func (e *Engine) pump() {
	defer close(e.pumpDone)
	for event := range e.engine.Events() {
		if event.Kind == "log" {
			continue
		} // Android consumes structured state, not raw Go logs.
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		e.callbacks.Lock()
		if e.enabled && e.sink != nil && event.Generation == e.generation {
			e.sink.OnEvent(string(data))
		}
		e.callbacks.Unlock()
	}
}

func (e *Engine) Start(requestJSON string) error {
	request, err := parseRequest(requestJSON)
	if err != nil {
		return bridgeError(err)
	}
	e.operations.Lock()
	defer e.operations.Unlock()
	e.callbacks.Lock()
	defer e.callbacks.Unlock()
	if err := e.engine.Start(request); err != nil {
		return bridgeError(err)
	}
	e.generation = e.engine.Snapshot().Generation
	e.enabled = true
	return nil
}

// Stop waits for an in-flight callback and suppresses queued/late events. It
// preserves the facade for a later explicit Start. No callback follows return.
func (e *Engine) Stop() error {
	e.operations.Lock()
	defer e.operations.Unlock()
	e.callbacks.Lock()
	e.enabled = false
	e.callbacks.Unlock()
	return bridgeError(e.engine.Stop())
}

// Close is idempotent and final. Hosts call it off the UI thread when their
// service is destroyed; the facade never retains an Activity.
func (e *Engine) Close() error {
	e.operations.Lock()
	defer e.operations.Unlock()
	e.callbacks.Lock()
	e.enabled, e.sink = false, nil
	e.callbacks.Unlock()
	err := e.engine.Close()
	if e.network != nil {
		e.network.close()
	}
	<-e.pumpDone
	return bridgeError(err)
}

func (e *Engine) ApplyConfig(requestJSON string) error {
	request, err := parseRequest(requestJSON)
	if err != nil {
		return bridgeError(err)
	}
	e.operations.Lock()
	defer e.operations.Unlock()
	return bridgeError(e.engine.ApplyConfig(request))
}

func (e *Engine) SnapshotJSON() string {
	data, _ := json.Marshal(e.engine.Snapshot())
	return string(data)
}

func (e *Engine) NetworkChanged(eventJSON string) error {
	var event struct {
		Sequence      string `json:"sequence"`
		NetworkHandle string `json:"network_handle"`
	}
	if len(eventJSON) > 4096 {
		return bridgeError(fmt.Errorf("network event exceeds 4 KiB"))
	}
	if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
		return bridgeError(err)
	}
	sequence, err := strconv.ParseUint(event.Sequence, 10, 64)
	if err != nil || sequence == 0 {
		return bridgeError(fmt.Errorf("invalid network sequence"))
	}
	e.operations.Lock()
	defer e.operations.Unlock()
	if sequence <= e.networkSequence {
		return nil
	}
	e.networkSequence = sequence
	return bridgeError(e.engine.NetworkChanged())
}

func (e *Engine) RenominateTransports() error {
	e.operations.Lock()
	defer e.operations.Unlock()
	return bridgeError(e.engine.RenominateTransports())
}

func Validate(requestJSON string) error {
	request, err := parseRequest(requestJSON)
	if err != nil {
		return bridgeError(err)
	}
	return bridgeError(request.Validate())
}

func parseRequest(text string) (core.Request, error) {
	if len(text) > 128*1024 {
		return core.Request{}, fmt.Errorf("request exceeds 128 KiB")
	}
	var wire struct {
		APIVersion int    `json:"api_version"`
		ServerURL  string `json:"server_url"`
		// Accepted for wire compatibility with older hosts. Configuration
		// ordering is provided by the serialized Engine API, not by a caller
		// maintained counter.
		LegacyRevision json.RawMessage `json:"config_revision"`
		Config         json.RawMessage `json:"config"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return core.Request{}, fmt.Errorf("invalid request JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return core.Request{}, fmt.Errorf("request must contain exactly one JSON object")
	}
	if wire.APIVersion != core.APIVersion {
		return core.Request{}, &core.Fault{Code: "api_version_mismatch", Message: "桥接 API 版本不匹配"}
	}
	if len(wire.Config) == 0 || wire.Config[0] != '{' {
		return core.Request{}, fmt.Errorf("config must be an object")
	}
	// JSON is a YAML subset; use the same strict field and endpoint parser as CLI.
	config, err := core.ParseConfig(wire.Config)
	if err != nil {
		return core.Request{}, err
	}
	return core.Request{ServerURL: wire.ServerURL, Config: config}, nil
}

func bridgeError(err error) error {
	if err == nil {
		return nil
	}
	fault := &core.Fault{Code: "invalid_request", Message: err.Error()}
	var classified *core.Fault
	if errors.As(err, &classified) {
		fault = classified
	}
	data, _ := json.Marshal(fault)
	return errors.New(string(data))
}
