package main

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

type synchronousHandle struct {
	started chan struct{}
	release chan struct{}
	data    []byte
}

func (h *synchronousHandle) Read(p []byte) (int, error) {
	close(h.started)
	<-h.release
	return copy(p, "late read"), nil
}

func (h *synchronousHandle) Write(p []byte) (int, error) {
	close(h.started)
	<-h.release
	h.data = bytes.Clone(p)
	return len(p), nil
}

func TestProcessAdaptersCancelSynchronousHandlesWithoutBufferRaces(t *testing.T) {
	for _, direction := range []string{"read", "write"} {
		t.Run(direction, func(t *testing.T) {
			handle := &synchronousHandle{started: make(chan struct{}), release: make(chan struct{})}
			queue := newProcessIO()
			buffer := []byte("original")
			result := make(chan error, 1)
			go func() {
				var err error
				if direction == "read" {
					_, err = (processReader{processIO: queue, source: handle}).Read(buffer)
				} else {
					_, err = (processWriter{processIO: queue, target: handle}).Write(buffer)
				}
				result <- err
			}()
			<-handle.started
			_ = queue.Close()
			select {
			case err := <-result:
				if !errors.Is(err, io.ErrClosedPipe) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("adapter waited on an uninterruptible raw syscall")
			}
			// A canceled caller may immediately reuse its buffer. The outstanding
			// syscall owns a copy and must not race with or observe this mutation.
			copy(buffer, "replaced")
			close(handle.release)
			<-queue.done
			if string(buffer) != "replaced" || direction == "write" && string(handle.data) != "original" {
				t.Fatalf("caller/raw buffers alias: buffer=%q output=%q", buffer, handle.data)
			}
		})
	}
}
