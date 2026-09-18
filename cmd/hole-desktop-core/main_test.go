package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type outputBuffer struct{ bytes.Buffer }

func (*outputBuffer) Close() error { return nil }

func TestHelpAndInvalidArgsKeepStdoutReserved(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{[]string{"--help"}, 0}, {[]string{"--unknown", "PRIVATE_VALUE"}, 2}} {
		var out outputBuffer
		var diagnostics bytes.Buffer
		code := run(tc.args, io.NopCloser(strings.NewReader("")), &out, &diagnostics)
		if code != tc.code || out.Len() != 0 || diagnostics.Len() == 0 || strings.Contains(diagnostics.String(), "PRIVATE_VALUE") {
			t.Fatalf("arguments: code=%d out=%q stderr=%q", code, out.String(), diagnostics.String())
		}
	}
}

func TestDesktopCoreHelper(t *testing.T) {
	if os.Getenv("HOLE_DESKTOP_CORE_HELPER") != "1" {
		return
	}
	os.Exit(run(nil, os.Stdin, os.Stdout, os.Stderr))
}

func TestProcessPipeEOFShutdownAndSignal(t *testing.T) {
	for _, action := range []string{"eof", "shutdown", "signal", "broken_output"} {
		t.Run(action, func(t *testing.T) {
			if action == "signal" && runtime.GOOS == "windows" {
				t.Skip("Windows parent exit is exercised through pipe EOF, not POSIX SIGTERM")
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestDesktopCoreHelper$")
			cmd.Env = append(os.Environ(), "HOLE_DESKTOP_CORE_HELPER=1")
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill() })
			lines := make(chan []byte, 8)
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				defer close(lines)
				scan := bufio.NewScanner(output)
				for scan.Scan() {
					lines <- bytes.Clone(scan.Bytes())
				}
			}()
			reply := func(id string) {
				t.Helper()
				select {
				case line := <-lines:
					var m struct {
						JSONRPC string          `json:"jsonrpc"`
						ID      string          `json:"id"`
						Result  json.RawMessage `json:"result"`
					}
					if json.Unmarshal(line, &m) != nil || m.JSONRPC != "2.0" || m.ID != id || len(m.Result) == 0 {
						t.Fatalf("invalid child stdout: %q", line)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("no response from child process")
				}
			}
			// Splitting one request across writes must not split protocol frames.
			_, _ = io.WriteString(input, `{"jsonrpc":"2.0","id":"hello",`)
			_, _ = io.WriteString(input, "\"method\":\"hello\"}\r\n")
			reply("hello")
			switch action {
			case "eof":
				// EOF also flushes a final complete JSON line without a newline.
				_, _ = io.WriteString(input, `{"jsonrpc":"2.0","id":"last","method":"snapshot"}`)
				_ = input.Close()
				reply("last")
			case "shutdown":
				_, _ = io.WriteString(input, "{\"jsonrpc\":\"2.0\",\"id\":\"bye\",\"method\":\"shutdown\"}\n")
				reply("bye")
				// Keep stdin open: shutdown must interrupt the next pending read.
			case "signal":
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			case "broken_output":
				_ = output.Close()
				_, _ = io.WriteString(input, "{\"jsonrpc\":\"2.0\",\"id\":\"last\",\"method\":\"snapshot\"}\n")
			}
			done := make(chan error, 1)
			go func() { <-readDone; done <- cmd.Wait() }()
			select {
			case err := <-done:
				if action == "broken_output" {
					if cmd.ProcessState.ExitCode() != 1 || !strings.Contains(diagnostics.String(), "output write failed") {
						t.Fatalf("broken pipe skipped cleanup/error handling: %v stderr=%s", err, diagnostics.String())
					}
				} else if err != nil || diagnostics.Len() != 0 {
					t.Fatalf("child exit: %v, stderr=%s", err, diagnostics.String())
				}
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Fatal("child outlived shutdown/EOF/signal with stdin still open")
			}
		})
	}
}
