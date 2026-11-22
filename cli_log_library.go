package main

import (
	"log"
	"strings"
)

// Some dependencies use the process-wide logger instead of core events. The
// reporter owns a separate logger so forwarding here cannot recurse or deadlock.
type cliLibraryLog struct {
	reporter *cliReporter
}

func (w *cliLibraryLog) Write(data []byte) (int, error) {
	w.reporter.mu.Lock()
	defer w.reporter.mu.Unlock()
	message := strings.TrimSuffix(string(data), "\n")
	if w.reporter.debug {
		defer w.reporter.logScope(cliLogContext{Component: "library"})()
		w.reporter.problem("debug/library", logLevelDebug, "lib "+logText(message))
	}
	// Raw library diagnostics cannot establish device or service failure.
	// Those failures arrive as classified core events with their context.
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
