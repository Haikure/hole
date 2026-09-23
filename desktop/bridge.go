package desktop

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"hole/core"
)

const (
	responseCapacity = 4
	eventCapacity    = 64
	writeTimeout     = 5 * time.Second
)

var (
	errRequestTooLarge = errors.New("desktop bridge: request exceeds 1 MiB")
	errInput           = errors.New("desktop bridge: input read failed")
	errOutput          = errors.New("desktop bridge: output write failed")
	errOutputTimeout   = errors.New("desktop bridge: output stalled for too long")
)

type engine interface {
	Start(core.Request) error
	ApplyConfig(core.Request) error
	Stop() error
	Close() error
	NetworkChanged() error
	RenominateTransports() error
	Snapshot() core.Snapshot
	Events() <-chan core.Event
}

// Serve owns both streams and one Engine for the duration of the connection.
// Construction and hello/validate/snapshot do not start network activity.
// EOF, context cancellation, output failure, or shutdown closes the Engine.
// The streams must unblock pending Read/Write calls when Close is called;
// use the child process's dedicated stdin/stdout pipes, not shared streams.
func Serve(ctx context.Context, input io.ReadCloser, output io.WriteCloser) error {
	return serve(ctx, input, output, core.NewEngine(core.Options{RetryNetwork: true}), writeTimeout)
}

type frame struct {
	data []byte
	err  error
}

type host struct {
	engine        engine
	eventsDropped uint64
}

func serve(parent context.Context, input io.ReadCloser, output io.WriteCloser, backend engine, timeout time.Duration) error {
	ctx, cancel := context.WithCancelCause(parent)
	frames := make(chan frame, 1)
	responses := make(chan []byte, responseCapacity)
	events := make(chan []byte, eventCapacity)
	readDone, writeDone, cleanupDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	context.AfterFunc(ctx, func() {
		defer close(cleanupDone)
		_ = input.Close()
		_ = output.Close()
		_ = backend.Close()
	})
	go func() {
		defer close(readDone)
		readFrames(ctx, input, frames)
	}()
	go func() {
		defer close(writeDone)
		writeFrames(ctx, cancel, output, responses, events, timeout)
	}()
	defer func() {
		cancel(context.Canceled)
		<-cleanupDone
		<-readDone
		<-writeDone
	}()

	h := &host{engine: backend}
	err := h.run(ctx, frames, responses, events)
	// Graceful EOF/shutdown drains responses (including the shutdown ack), but
	// not a potentially stale event backlog. Each write still has a deadline.
	_ = backend.Close()
	close(responses)
	<-writeDone
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

func readFrames(ctx context.Context, input io.Reader, frames chan<- frame) {
	send := func(f frame) bool {
		select {
		case frames <- f:
			return true
		case <-ctx.Done():
			return false
		}
	}
	s := bufio.NewScanner(input)
	s.Buffer(make([]byte, 4096), MaxRequestBytes+2) // LF or CRLF is not payload.
	for s.Scan() {
		if len(s.Bytes()) > MaxRequestBytes {
			send(frame{err: errRequestTooLarge})
			return
		}
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		if !send(frame{data: bytes.Clone(s.Bytes())}) {
			return
		}
	}
	err := s.Err()
	switch {
	case errors.Is(err, bufio.ErrTooLong):
		err = errRequestTooLarge
	case err != nil:
		err = errInput
	default:
		err = io.EOF
	}
	send(frame{err: err})
}

func writeFrames(ctx context.Context, cancel context.CancelCauseFunc, output io.Writer, responses, events <-chan []byte, timeout time.Duration) {
	write := func(data []byte) bool {
		timer := time.AfterFunc(timeout, func() { cancel(errOutputTimeout) })
		defer timer.Stop()
		for len(data) > 0 {
			n, err := output.Write(data)
			if err != nil || n <= 0 || n > len(data) {
				cancel(errOutput)
				return false
			}
			data = data[n:]
		}
		return ctx.Err() == nil
	}
	for {
		if ctx.Err() != nil {
			return
		}
		// Control replies are never dropped and take priority over notifications.
		select {
		case data, ok := <-responses:
			if !ok || !write(data) {
				return
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case data, ok := <-responses:
			if !ok || !write(data) {
				return
			}
		case data := <-events:
			if !write(data) {
				return
			}
		}
	}
}

func (h *host) run(ctx context.Context, frames <-chan frame, responses, events chan<- []byte) error {
	coreEvents := h.engine.Events()
	reply := func(r response) bool {
		data, err := encodeMessage(r)
		if err != nil {
			data, _ = encodeMessage(response{JSONRPC: "2.0", ID: r.ID, Error: rpcFault(internalError, "response_too_large", "响应超过大小限制")})
		}
		select {
		case responses <- data:
			return true
		case <-ctx.Done():
			return false
		}
	}
	handle := func(f frame) (bool, error) {
		if f.err != nil {
			if errors.Is(f.err, io.EOF) {
				return true, nil
			}
			if errors.Is(f.err, errRequestTooLarge) {
				reply(response{JSONRPC: "2.0", Error: rpcFault(invalidRequest, "request_too_large", "请求超过 1 MiB，连接结束")})
			}
			return true, f.err
		}
		r, fault := decodeRequest(f.data)
		var result any
		shutdown := false
		if fault == nil {
			result, fault, shutdown = h.dispatch(r)
		}
		if !reply(response{JSONRPC: "2.0", ID: r.ID, Result: result, Error: fault}) {
			return true, context.Cause(ctx)
		}
		return shutdown, nil
	}
	for {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		// A busy event stream should not starve lifecycle requests.
		select {
		case f := <-frames:
			if stop, err := handle(f); stop {
				return err
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case f := <-frames:
			if stop, err := handle(f); stop {
				return err
			}
		case event, ok := <-coreEvents:
			if !ok {
				coreEvents = nil
				continue
			}
			// Match the Android facade: diagnostics use structured state, not
			// raw Go logs. Intentionally filtered logs are not queue loss.
			if event.Kind == "log" {
				continue
			}
			data, err := encodeMessage(notification{JSONRPC: "2.0", Method: "event", Params: event})
			if err != nil {
				h.eventsDropped++
				continue
			}
			select {
			case events <- data:
			default:
				h.eventsDropped++
			}
		}
	}
}

func (h *host) dispatch(r request) (any, *rpcError, bool) {
	accepted := struct {
		Accepted bool `json:"accepted"`
	}{true}
	if r.Method == "decode_cli_config" || r.Method == "encode_cli_config" {
		result, fault := exchangeConfig(r.Method, r.Params)
		return result, fault, false
	}
	if r.Method == "start" || r.Method == "apply_config" || r.Method == "validate" {
		cfg, fault := engineRequest(r.Params)
		if fault != nil {
			return nil, fault, false
		}
		var err error
		switch r.Method {
		case "start":
			err = h.engine.Start(cfg)
		case "apply_config":
			err = h.engine.ApplyConfig(cfg)
		case "validate":
			return struct {
				Valid bool `json:"valid"`
			}{true}, nil, false
		}
		if err != nil {
			return nil, operationFault(err), false
		}
		return accepted, nil, false
	}
	// Unknown methods are reported independently of their params schema.
	switch r.Method {
	case "hello", "snapshot", "stop", "network_changed", "renominate_transports", "shutdown":
	default:
		return nil, rpcFault(methodNotFound, "method_not_found", "方法不存在"), false
	}
	if !noParams(r.Params) {
		return nil, rpcFault(invalidParams, "invalid_params", "此方法省略 params 或传入空对象"), false
	}
	var err error
	switch r.Method {
	case "hello":
		return helloResult{
			BridgeVersion: ProtocolVersion, APIVersion: core.APIVersion, CoreVersion: core.CoreVersion,
			Methods: []string{"hello", "validate", "start", "apply_config", "stop", "snapshot", "network_changed", "renominate_transports", "decode_cli_config", "encode_cli_config", "shutdown"},
			Limits:  protocolLimits{MaxRequestBytes, MaxConfigBytes, MaxOutputBytes, eventCapacity, responseCapacity, int(writeTimeout / time.Millisecond)},
		}, nil, false
	case "snapshot":
		return snapshotResult{Snapshot: h.engine.Snapshot(), BridgeEventsDropped: h.eventsDropped}, nil, false
	case "stop":
		err = h.engine.Stop()
	case "network_changed":
		err = h.engine.NetworkChanged()
	case "renominate_transports":
		err = h.engine.RenominateTransports()
	case "shutdown":
		err = h.engine.Close()
	}
	if err != nil {
		return nil, operationFault(err), false
	}
	return accepted, nil, r.Method == "shutdown"
}
