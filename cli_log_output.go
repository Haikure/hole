package main

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"hole/core"
)

type cliLogContext struct {
	Component           string `json:"component"`
	State               string `json:"state,omitempty"`
	MappingID           string `json:"mapping_id,omitempty"`
	Protocol            string `json:"protocol,omitempty"`
	Peer                string `json:"peer,omitempty"`
	Path                string `json:"path,omitempty"`
	Target              string `json:"target,omitempty"`
	RemoteAddress       string `json:"remote_address,omitempty"`
	LocalAddress        string `json:"local_address,omitempty"`
	AddressFamily       string `json:"address_family,omitempty"`
	ErrorCode           string `json:"error_code,omitempty"`
	ErrorMessage        string `json:"error_message,omitempty"`
	Phase               string `json:"phase,omitempty"`
	TransportGeneration uint64 `json:"transport_generation,omitempty"`
	SessionID           string `json:"session_id,omitempty"`
	Stage               string `json:"stage,omitempty"`
	ElapsedMS           int64  `json:"elapsed_ms,omitempty"`
	RTTMS               int64  `json:"rtt_ms,omitempty"`
	Count               uint64 `json:"count,omitempty"`
}

type cliLogRecord struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
	cliLogContext
}

func logEventContext(e core.Event) cliLogContext {
	c := cliLogContext{Component: e.Kind, State: e.State, MappingID: e.MappingID, Protocol: e.Protocol,
		Peer: e.Peer, Path: e.Path, Target: e.Target, Phase: e.Phase, TransportGeneration: e.TransportGeneration,
		SessionID: e.SessionID, Stage: e.Stage}
	if e.Error != nil {
		c.ErrorCode, c.ErrorMessage = e.Error.Code, e.Error.Message
	}
	return c
}

// Scopes are used while holding reporter.mu, or before goroutines are started.
func (r *cliReporter) logScope(c cliLogContext) func() {
	previous := r.context
	r.context = c
	return func() { r.context = previous }
}

func (r *cliReporter) redactDiagnostic(value string) string {
	return r.redactor.Replace(r.urlRedactor.Replace(value))
}

func (r *cliReporter) writeLog(level, message string) {
	if level == logLevelDebug && !r.debug {
		return
	}
	if level == logLevelDebug {
		message = r.redactDiagnostic(message)
	}
	if r.format != "json" {
		if level != logLevelDebug {
			switch r.context.Component {
			case "peer":
				message = "设备 " + message
			case "mapping", "listener":
				message = "映射 " + message
			case "session":
				message = "服务 " + message
			}
		}
		r.logger.Print(level + message)
		return
	}
	c := r.context
	if c.Component == "" {
		c.Component = "cli"
	}
	if level == logLevelDebug {
		for _, value := range []*string{&c.Component, &c.State, &c.MappingID, &c.Protocol, &c.Peer, &c.Path, &c.Target,
			&c.RemoteAddress, &c.LocalAddress, &c.AddressFamily, &c.ErrorCode, &c.ErrorMessage, &c.Phase, &c.SessionID, &c.Stage} {
			*value = r.redactDiagnostic(*value)
		}
	}
	// Ordinary status records preserve their content even when a name or address
	// happens to match a credential. Only DEBUG diagnostics redact known secrets.
	// JSON encoding keeps each record on one line, with local time like text logs.
	data, _ := json.Marshal(cliLogRecord{Time: time.Now().Format("15:04:05"), Level: strings.TrimSpace(level), Message: message, cliLogContext: c})
	if r.jsonLogger == nil {
		r.jsonLogger = log.New(r.logger.Writer(), "", 0)
	}
	r.jsonLogger.Print(string(data))
}
