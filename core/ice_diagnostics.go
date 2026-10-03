package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pion/logging"
)

const maxICEDiagnosticLogs = 64

// 每次尝试保留独立的脱敏上下文，覆盖配置重配前的秘密和动态 TURN / ICE 凭据。
// 接收 warning/error 及明确的发送失败，不启用包含协议报文的 debug/trace。
type iceDiagnostics struct {
	ctx   context.Context
	ready iceSignalMessage
	emit  func(Event)

	mu            sync.Mutex
	secrets       map[string]bool
	replacer      *strings.Replacer
	logs          int
	seenTransient map[string]bool
}

func newICEDiagnostics(ctx context.Context, ready iceSignalMessage, cfg Config, emit func(Event)) *iceDiagnostics {
	d := &iceDiagnostics{ctx: ctx, ready: ready, emit: emit, secrets: make(map[string]bool)}
	d.addSecrets(cfg.Password, cfg.Token, cfg.TURN.Username, cfg.TURN.Credential)
	return d
}

func (d *iceDiagnostics) addSecrets(secrets ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		quoted := strconv.Quote(secret)
		for _, spelling := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), quoted[1 : len(quoted)-1], hex.EncodeToString([]byte(secret))} {
			d.secrets[spelling] = true
		}
	}
	spellings := make([]string, 0, len(d.secrets))
	for spelling := range d.secrets {
		spellings = append(spellings, spelling)
	}
	// 一次替换且长串优先，避免短秘密破坏长秘密或再次替换脱敏标记。
	sort.Slice(spellings, func(i, j int) bool { return len(spellings[i]) > len(spellings[j]) })
	pairs := make([]string, 0, 2*len(spellings))
	for _, spelling := range spellings {
		pairs = append(pairs, spelling, "[redacted]")
	}
	d.replacer = strings.NewReplacer(pairs...)
}

func (d *iceDiagnostics) redact(text string) string {
	d.mu.Lock()
	text = d.replacer.Replace(text)
	d.mu.Unlock()
	// 完整脱敏之后再复用事件长度限制，不能先截断凭据。
	return redact(text, Config{})
}

func (d *iceDiagnostics) event(state, scope string) Event {
	return Event{Kind: "log", State: state, Stage: scope, Peer: d.ready.PeerDevice,
		Profile: ProfileICE, Phase: d.ready.Phase, TransportGeneration: d.ready.TransportGeneration}
}

func (d *iceDiagnostics) log(state, scope, message string) {
	if d.emit == nil || d.ctx.Err() != nil {
		return
	}
	if message == "Failed to ping without candidate pairs. Connection is not possible yet." || strings.HasPrefix(message, "Failed to send packet:") {
		key := scope + ":" + d.redact(message)
		d.mu.Lock()
		if d.seenTransient == nil {
			d.seenTransient = make(map[string]bool)
		}
		seen := d.seenTransient[key]
		if len(d.seenTransient) < maxICEDiagnosticLogs {
			d.seenTransient[key] = true
		}
		d.mu.Unlock()
		if seen {
			return
		}
		if message == "Failed to ping without candidate pairs. Connection is not possible yet." {
			state, message = "info", "等待本地与对端候选形成候选对"
		}
	}
	d.mu.Lock()
	n := d.logs
	if n <= maxICEDiagnosticLogs {
		d.logs++
	}
	d.mu.Unlock()
	if n > maxICEDiagnosticLogs {
		return
	}
	if n == maxICEDiagnosticLogs {
		message = "本次 ICE 尝试的底层日志已达上限，后续底层日志省略；最终失败原因仍会输出"
	}
	e := d.event(state, scope)
	e.Message = d.redact(message)
	d.emit(e)
}

func (d *iceDiagnostics) failed(err error) {
	if d == nil || d.emit == nil || d.ctx.Err() != nil {
		return
	}
	e := d.event("failed", "ice")
	e.Error = classifyError(err)
	e.Error.Message = d.redact(e.Error.Message)
	d.emit(e)
}

func (d *iceDiagnostics) NewLogger(scope string) logging.LeveledLogger {
	// 不读取 PION_LOG_* 环境变量，确保外部配置不能打开报文级日志。
	return &iceDiagnosticLogger{LeveledLogger: logging.NewDefaultLeveledLoggerForScope(scope, logging.LogLevelDisabled, io.Discard), diagnostics: d, scope: scope}
}

type iceDiagnosticLogger struct {
	logging.LeveledLogger
	diagnostics *iceDiagnostics
	scope       string
}

func (l *iceDiagnosticLogger) Warn(message string) {
	l.diagnostics.log("warning", l.scope, message)
}
func (l *iceDiagnosticLogger) Warnf(format string, args ...any) {
	l.Warn(fmt.Sprintf(format, args...))
}
func (l *iceDiagnosticLogger) Error(message string) {
	l.diagnostics.log("error", l.scope, message)
}
func (l *iceDiagnosticLogger) Errorf(format string, args ...any) {
	l.Error(fmt.Sprintf(format, args...))
}

// Pion reports socket/permission write failures at Info. Admit only this
// known error prefix, without enabling its credential-bearing protocol logs.
func (l *iceDiagnosticLogger) Info(message string) {
	if strings.HasPrefix(message, "Failed to send packet:") {
		l.diagnostics.log("warning", l.scope, message)
	}
}
func (l *iceDiagnosticLogger) Infof(format string, args ...any) {
	if strings.HasPrefix(format, "Failed to send packet:") {
		l.Info(fmt.Sprintf(format, args...))
	}
}
