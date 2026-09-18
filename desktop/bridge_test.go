package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"hole/core"
)

type fakeEngine struct {
	mu       sync.Mutex
	calls    []string
	requests []core.Request
	err      error
	closed   bool
	events   chan core.Event
	state    core.Snapshot
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{events: make(chan core.Event, 16), state: core.Snapshot{APIVersion: core.APIVersion, EngineState: "stopped"}}
}
func (e *fakeEngine) call(name string, r *core.Request) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, name)
	if r != nil {
		e.requests = append(e.requests, *r)
	}
	return e.err
}
func (e *fakeEngine) Start(r core.Request) error       { return e.call("start", &r) }
func (e *fakeEngine) ApplyConfig(r core.Request) error { return e.call("apply_config", &r) }
func (e *fakeEngine) Stop() error                      { return e.call("stop", nil) }
func (e *fakeEngine) NetworkChanged() error            { return e.call("network_changed", nil) }
func (e *fakeEngine) Events() <-chan core.Event        { return e.events }
func (e *fakeEngine) Snapshot() core.Snapshot          { return e.state }
func (e *fakeEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		e.calls = append(e.calls, "close")
		close(e.events)
	}
	return nil
}

type bufferCloser struct{ bytes.Buffer }

func (*bufferCloser) Close() error { return nil }

type shortReader struct{ io.Reader }

func (r shortReader) Read(p []byte) (int, error) {
	return r.Reader.Read(p[:min(len(p), 3)])
}
func (shortReader) Close() error { return nil }

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) {
	return w.Buffer.Write(p[:min(len(p), 7)])
}
func (*shortWriter) Close() error { return nil }

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *string         `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func exchange(t *testing.T, input string, backend engine) ([]wireMessage, error) {
	t.Helper()
	var output bufferCloser
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := serve(ctx, io.NopCloser(strings.NewReader(input)), &output, backend, time.Second)
	var messages []wireMessage
	s := bufio.NewScanner(&output)
	s.Buffer(make([]byte, 4096), MaxOutputBytes+2)
	for s.Scan() {
		var m wireMessage
		if decodeErr := json.Unmarshal(s.Bytes(), &m); decodeErr != nil || m.JSONRPC != "2.0" {
			t.Fatalf("non-protocol stdout: %q (%v)", s.Text(), decodeErr)
		}
		if m.Method == "" {
			messages = append(messages, m)
		}
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	return messages, err
}

func TestLifecycleDispatchAndShutdownFlush(t *testing.T) {
	e := newFakeEngine()
	input := wireRequest("1", "hello", "") + wireRequest("2", "validate", validParams()) +
		wireRequest("3", "start", validParams()) + wireRequest("4", "apply_config", validParams()) +
		wireRequest("5", "snapshot", "") + wireRequest("6", "network_changed", "{}") +
		wireRequest("7", "stop", "") + wireRequest("8", "shutdown", "") + wireRequest("9", "start", validParams())
	messages, err := exchange(t, input, e)
	if err != nil || len(messages) != 8 {
		t.Fatalf("lost responses: %d %v", len(messages), err)
	}
	for i, m := range messages {
		if m.ID == nil || m.Error != nil || len(m.Result) == 0 {
			t.Fatalf("response %d: %+v", i, m)
		}
	}
	var hello helloResult
	if err := json.Unmarshal(messages[0].Result, &hello); err != nil || hello.BridgeVersion != ProtocolVersion || hello.APIVersion != core.APIVersion || hello.Limits.RequestBytes != MaxRequestBytes {
		t.Fatalf("hello: %+v %v", hello, err)
	}
	if got := strings.Join(e.calls, ","); got != "start,apply_config,network_changed,stop,close" {
		t.Fatalf("unexpected lifecycle calls: %s", got)
	}
	if len(e.requests) != 2 {
		t.Fatal("requests were not dispatched")
	}
}

func TestReadOnlyRequestsAndEmptyEOFDoNotStartEngine(t *testing.T) {
	for _, input := range []string{"", "\n\r\n", wireRequest("1", "hello", "") + wireRequest("2", "validate", validParams()) + wireRequest("3", "snapshot", "")} {
		e := newFakeEngine()
		if _, err := exchange(t, input, e); err != nil {
			t.Fatal(err)
		}
		if strings.Join(e.calls, ",") != "close" {
			t.Fatalf("read-only request mutated engine: %v", e.calls)
		}
	}
}

func TestPartialIOPreservesFramesAndResponseOrder(t *testing.T) {
	input := shortReader{strings.NewReader(wireRequest("first", "hello", "") + wireRequest("second", "snapshot", "") + wireRequest("last", "shutdown", ""))}
	var output shortWriter
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := serve(ctx, input, &output, newFakeEngine(), time.Second); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for _, id := range []string{"first", "second", "last"} {
		var reply wireMessage
		if err := decoder.Decode(&reply); err != nil || reply.ID == nil || *reply.ID != id || reply.Error != nil {
			t.Fatalf("partial I/O lost response %q: %+v %v", id, reply, err)
		}
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing or interleaved response data")
	}
}

func TestOversizedSnapshotReturnsAnErrorAndKeepsControlAvailable(t *testing.T) {
	e := newFakeEngine()
	e.state.EngineState = strings.Repeat("x", MaxOutputBytes)
	ms, err := exchange(t, wireRequest("1", "snapshot", "")+wireRequest("2", "shutdown", ""), e)
	if err != nil || len(ms) != 2 || ms[0].Error == nil || ms[0].Error.Code != internalError || ms[1].Error != nil || !e.closed {
		t.Fatalf("oversized snapshot corrupted the stream: %+v %v", ms, err)
	}
}

func TestProtocolErrorsRecoverWithoutExecutingInvalidRequests(t *testing.T) {
	e := newFakeEngine()
	input := "{\n" + wireRequest("1", "missing", "") + wireRequest("2", "shutdown", `{"extra":true}`) +
		wireRequest("3", "start", `{}`) + `{"jsonrpc":"2.0","method":"shutdown"}` + "\n" + wireRequest("4", "hello", "")
	ms, err := exchange(t, input, e)
	if err != nil || len(ms) != 6 {
		t.Fatalf("bad recovery: %d %v", len(ms), err)
	}
	for i, code := range []int{parseError, methodNotFound, invalidParams, invalidParams, invalidRequest} {
		if ms[i].Error == nil || ms[i].Error.Code != code {
			t.Fatalf("error %d: %+v", i, ms[i])
		}
	}
	if ms[5].Error != nil || ms[5].ID == nil || *ms[5].ID != "4" || strings.Join(e.calls, ",") != "close" {
		t.Fatalf("invalid command acted or hello was lost: %v %+v", e.calls, ms[5])
	}
}

func TestFramingLimitsCRLFAndFinalLine(t *testing.T) {
	base := strings.TrimSuffix(wireRequest("1", "hello", ""), "\n")
	for _, ending := range []string{"\n", "\r\n", ""} {
		input := base + strings.Repeat(" ", MaxRequestBytes-len(base)) + ending
		ms, err := exchange(t, input, newFakeEngine())
		if err != nil || len(ms) != 1 || ms[0].Error != nil {
			t.Fatalf("valid boundary rejected (%q): %+v %v", ending, ms, err)
		}
	}
	for _, ending := range []string{"\n", "\r\n", ""} {
		e := newFakeEngine()
		input := base + strings.Repeat(" ", MaxRequestBytes+1-len(base)) + ending
		if ending != "" {
			input += wireRequest("2", "start", validParams())
		}
		ms, err := exchange(t, input, e)
		if !errors.Is(err, errRequestTooLarge) || len(ms) != 1 || ms[0].Error == nil || ms[0].Error.Data.Code != "request_too_large" || !e.closed {
			t.Fatalf("oversized input: %+v %v", ms, err)
		}
		if strings.Join(e.calls, ",") != "close" {
			t.Fatal("processed a request after the frame limit")
		}
	}
}

func TestCoreErrorsAndSecretRedaction(t *testing.T) {
	for _, underlying := range []error{
		&core.Fault{Code: "network_error", Message: "网络操作失败"},
		errors.New("PRIVATE_PASSWORD"),
	} {
		e := newFakeEngine()
		e.err = underlying
		ms, err := exchange(t, wireRequest("1", "start", validParams()), e)
		if err != nil || len(ms) != 1 || ms[0].Error == nil || ms[0].Error.Code != coreError {
			t.Fatalf("core error: %+v %v", ms, err)
		}
		data, _ := json.Marshal(ms)
		if bytes.Contains(data, []byte("PRIVATE_")) {
			t.Fatal("unclassified error exposed a secret")
		}
	}
}

func waitServe(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not terminate and join its workers")
		return nil
	}
}

func TestCancellationAndBrokenOutputReleaseOwnedStreams(t *testing.T) {
	for _, kind := range []string{"cancel", "broken_output", "stalled_output"} {
		t.Run(kind, func(t *testing.T) {
			input, inputWriter := io.Pipe()
			outputReader, output := io.Pipe()
			defer inputWriter.Close()
			defer outputReader.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := newFakeEngine()
			done := make(chan error, 1)
			go func() { done <- serve(ctx, input, output, e, 50*time.Millisecond) }()
			want := context.Canceled
			if kind == "cancel" {
				// Cancellation must interrupt an incomplete frame, too.
				_, _ = io.WriteString(inputWriter, `{"jsonrpc":`)
				cancel()
			} else {
				if kind == "broken_output" {
					_ = outputReader.Close()
					want = errOutput
				} else {
					want = errOutputTimeout
				}
				_, _ = io.WriteString(inputWriter, wireRequest("1", "hello", ""))
			}
			if err := waitServe(t, done); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			if !e.closed {
				t.Fatal("engine outlived its control connection")
			}
			if _, err := inputWriter.Write([]byte("x")); err == nil {
				t.Fatal("input ownership was not released")
			}
		})
	}
}

func TestEventBackpressureIsBoundedAndSnapshotReportsLoss(t *testing.T) {
	e := newFakeEngine()
	e.events = make(chan core.Event)
	h := &host{engine: e}
	frames := make(chan frame)
	responses := make(chan []byte, 2)
	events := make(chan []byte, eventCapacity)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx, frames, responses, events) }()
	e.events <- core.Event{Kind: "log", Message: "raw log is filtered like Android"}
	for i := 1; i <= eventCapacity+10; i++ {
		select {
		case e.events <- core.Event{Kind: "transport", State: "changed", Sequence: uint64(i)}:
		case <-ctx.Done():
			t.Fatal("event forwarding blocked the core")
		}
	}
	frames <- frame{data: []byte(wireRequest("snapshot", "snapshot", ""))}
	var reply wireMessage
	if err := json.Unmarshal(<-responses, &reply); err != nil {
		t.Fatal(err)
	}
	var snapshot snapshotResult
	if err := json.Unmarshal(reply.Result, &snapshot); err != nil || snapshot.BridgeEventsDropped != 10 {
		t.Fatalf("loss counter: %+v %v", snapshot, err)
	}
	first := <-events
	var notice wireMessage
	if err := json.Unmarshal(first, &notice); err != nil || notice.Method != "event" || notice.ID != nil || !bytes.Contains(notice.Params, []byte(`"sequence":"1"`)) {
		t.Fatalf("notification: %s %v", first, err)
	}
	frames <- frame{data: []byte(wireRequest("shutdown", "shutdown", ""))}
	if err := waitServe(t, done); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || !e.closed {
		t.Fatal("shutdown reply was lost under event backpressure")
	}
}

func TestStoppedRealCoreRetainsConfigurationAndDoesNotExposeCredentials(t *testing.T) {
	e := core.NewEngine(core.Options{})
	input := wireRequest("1", "apply_config", validParams()) + wireRequest("2", "snapshot", "") +
		wireRequest("3", "apply_config", validParams())
	ms, err := exchange(t, input, e)
	if err != nil || len(ms) != 3 {
		t.Fatalf("real core: %+v %v", ms, err)
	}
	var state snapshotResult
	if err := json.Unmarshal(ms[1].Result, &state); err != nil {
		t.Fatal(err)
	}
	if !state.Snapshot.Configured || state.Snapshot.RunRequested || state.Snapshot.Generation != 0 {
		t.Fatalf("stopped configuration changed: %+v", state)
	}
	if ms[2].Error != nil {
		t.Fatalf("deprecated config_revision should not affect ApplyConfig: %+v", ms[2].Error)
	}
	data, _ := json.Marshal(ms)
	if bytes.Contains(data, []byte("PRIVATE_")) {
		t.Fatal("snapshot reflected credentials")
	}
}
