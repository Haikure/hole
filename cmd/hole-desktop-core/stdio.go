package main

import (
	"bytes"
	"io"
	"sync"
)

// Inherited stdio may be synchronous OS handles. os.File.Close need not
// interrupt a Read/Write already blocked on such a handle (including stdin on
// Linux and anonymous pipes on Windows). At this *process entry point* only,
// isolate each direction behind one worker so cancellation can join the bridge
// and cleanly stop Core before main calls os.Exit. A pending raw syscall then
// ends with the process; the reusable desktop.Serve API still joins all of its
// own workers and requires genuinely interruptible streams.
//
// Do not use these adapters for an embedded, restartable in-process host. They
// deliberately leave raw stdin/stdout ownership with the process, and cap any
// pending raw I/O to one operation per direction.
type processIO struct {
	jobs   chan ioJob
	closed chan struct{}
	done   chan struct{}
	once   sync.Once
}

type ioResult struct {
	n   int
	err error
}

type ioJob struct {
	perform func() ioResult
	result  chan ioResult
}

func newProcessIO() *processIO {
	p := &processIO{jobs: make(chan ioJob), closed: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		for {
			select {
			case <-p.closed:
				return
			case job := <-p.jobs:
				select {
				case <-p.closed:
					return
				default:
				}
				job.result <- job.perform()
			}
		}
	}()
	return p
}

func (p *processIO) execute(perform func() ioResult) ioResult {
	job := ioJob{perform: perform, result: make(chan ioResult, 1)}
	select {
	case <-p.closed:
		return ioResult{err: io.ErrClosedPipe}
	case p.jobs <- job:
	}
	select {
	case <-p.closed:
		return ioResult{err: io.ErrClosedPipe}
	case result := <-job.result:
		return result
	}
}

func (p *processIO) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

type processReader struct {
	*processIO
	source io.Reader
}

func (p processReader) Read(dst []byte) (int, error) {
	// Cancellation returns before a synchronous syscall necessarily finishes;
	// never let that syscall keep writing into a caller-owned buffer.
	buffer := make([]byte, len(dst))
	result := p.execute(func() ioResult {
		n, err := p.source.Read(buffer)
		return ioResult{n, err}
	})
	if result.n > 0 {
		copy(dst, buffer[:result.n])
	}
	return result.n, result.err
}

type processWriter struct {
	*processIO
	target io.Writer
}

func (p processWriter) Write(src []byte) (int, error) {
	buffer := bytes.Clone(src)
	result := p.execute(func() ioResult {
		n, err := p.target.Write(buffer)
		return ioResult{n, err}
	})
	return result.n, result.err
}
