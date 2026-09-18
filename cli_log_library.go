package main

import (
	"log"
	"strings"
	"sync"
)

// Some dependencies use the process-wide logger instead of core events. The
// reporter owns a separate logger so forwarding here cannot recurse or deadlock.
type cliLibraryLog struct {
	reporter *cliReporter
	notice   sync.Once
}

func (w *cliLibraryLog) Write(data []byte) (int, error) {
	message := strings.TrimSuffix(string(data), "\n")
	if w.reporter.debug {
		w.reporter.debugf("网络库：%s", logText(message))
		return len(data), nil
	}
	// ICE owns its sockets; quic-go sees a packet adapter, not a raw UDPConn.
	// This expected capability notice is not a failure to connect or transfer.
	for _, direction := range []string{"receive", "send"} {
		if strings.HasPrefix(message, "connection doesn't allow setting of "+direction+" buffer size. Not a *net.UDPConn?") {
			return len(data), nil
		}
	}
	w.notice.Do(func() {
		w.reporter.printf("【提示】网络库报告了运行诊断，可加 -debug 查看详情")
	})
	return len(data), nil
}

func (r *cliReporter) captureLibraryLogs() func() {
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(&cliLibraryLog{reporter: r})
	return func() {
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	}
}
