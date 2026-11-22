package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"hole/core"
)

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ltime)
	log.SetPrefix("")
	configPath := flag.String("config", "config.yaml", "配置文件")
	allowLocalWS := flag.Bool("allow-insecure-signal", false, "允许 ws:// 信令, 仅本地测试")
	debug := flag.Bool("debug", false, "输出调试日志")
	logFormat := flag.String("log-format", "text", "日志格式: text 或 json，均写入 stdout")
	version := flag.Bool("version", false, "显示共享核心版本")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "用法：%s [-config config.yaml] [选项]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *version {
		fmt.Println(core.CoreVersion)
		return
	}
	options := cliOptions{allowLocalWS: *allowLocalWS, debug: *debug, logFormat: *logFormat}
	if err := run(*configPath, options); err != nil && !errors.Is(err, context.Canceled) {
		var reported *cliReportedError
		if !errors.As(err, &reported) {
			reporter := newCLIReporter(log.New(log.Writer(), log.Prefix(), log.Flags()), core.Request{})
			reporter.format = options.logFormat
			reporter.context = cliLogContext{Component: "startup", State: "error"}
			reporter.errorf("%s", logText(err.Error()))
		}
		os.Exit(1)
	}
}

// Runtime failures are rendered by the reporter, including diagnostic details.
// Keep the exit status without printing the raw error a second time.
type cliReportedError struct{ err error }

func (e *cliReportedError) Error() string { return e.err.Error() }
func (e *cliReportedError) Unwrap() error { return e.err }

type cliOptions struct {
	allowLocalWS bool
	debug        bool
	logFormat    string
}

func (o cliOptions) validate() error {
	if o.logFormat != "" && o.logFormat != "text" && o.logFormat != "json" {
		return fmt.Errorf("不支持的日志格式 %q，-log-format 仅支持 text 或 json", o.logFormat)
	}
	return nil
}

// Prepare and validate everything before constructing an engine or opening a socket.
func cliRequest(data []byte, options cliOptions) (core.Request, error) {
	if err := options.validate(); err != nil {
		return core.Request{}, err
	}
	cfg, err := core.ParseConfig(data)
	if err != nil {
		return core.Request{}, err
	}
	serverURL := strings.TrimSpace(cfg.ServerURL)
	if serverURL == "" {
		return core.Request{}, fmt.Errorf("配置缺少 server_url, 在 YAML 中填写 server_url: wss://HOST/ws")
	}
	// The request envelope is the single authority inside the shared engine.
	cfg.ServerURL = ""
	if options.allowLocalWS {
		cfg.Transport.AllowInsecureSignal = true
	}
	request := core.Request{ServerURL: serverURL, Config: cfg}
	return request, request.Validate()
}

func run(configPath string, options cliOptions) error {
	if err := options.validate(); err != nil {
		return err
	}
	// Subscribe before reading so a save during startup cannot be missed.
	watcher, err := newCLIConfigWatcher(configPath)
	if err != nil {
		return err
	}
	defer watcher.Close()
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	request, err := cliRequest(data, options)
	if err != nil {
		return err
	}
	reporter := newCLIReporter(log.New(log.Writer(), log.Prefix(), log.Flags()), request)
	reporter.debug = options.debug
	reporter.format = options.logFormat
	restoreLibraryLogs := reporter.captureLibraryLogs()
	defer restoreLibraryLogs()
	reporter.startup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	engine := core.NewEngine(core.Options{AllowRelayServe: true})
	defer engine.Close()
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		reporter.follow(engine)
	}()
	err = engine.Start(request)
	if err == nil {
		watchCtx, cancelWatch := context.WithCancel(ctx)
		watchDone := make(chan struct{})
		reloader := cliConfigReloader{path: configPath, options: options, current: request, reporter: reporter, apply: engine.ApplyConfig}
		go func() {
			defer close(watchDone)
			watcher.run(watchCtx, reloader.reload, func(err error) {
				reporter.mu.Lock()
				defer reporter.mu.Unlock()
				defer reporter.logScope(cliLogContext{Component: "config", State: "error"})()
				reporter.warn("配置监听异常: %s", logText(err.Error()))
			})
		}()
		err = engine.Wait(ctx)
		cancelWatch()
		<-watchDone
	}
	_ = engine.Close()
	<-logsDone
	if err != nil && !errors.Is(err, context.Canceled) {
		var fault *core.Fault
		if !errors.As(err, &fault) {
			fault = &core.Fault{Code: "network_error", Message: err.Error()}
		}
		if !reporter.failureReported {
			reporter.event(core.Event{Kind: "engine", State: "error", Error: fault})
		}
		return &cliReportedError{err}
	}
	return err
}
