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
	transport := flag.String("transport", "", "连接方式 auto、ice 或 ipv6, 覆盖配置文件")
	allowLocalWS := flag.Bool("allow-insecure-signal", false, "允许 ws:// 信令, 仅本地测试")
	debug := flag.Bool("debug", false, "输出调试日志")
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
	if err := run(*configPath, cliOptions{transport: *transport, allowLocalWS: *allowLocalWS, debug: *debug}); err != nil && !errors.Is(err, context.Canceled) {
		var reported *cliReportedError
		if !errors.As(err, &reported) {
			log.Printf("ERROR %s", logText(err.Error()))
		}
		os.Exit(1)
	}
}

// Runtime failures are rendered by the reporter, including redaction and debug
// details. Keep the exit status without printing the raw error a second time.
type cliReportedError struct{ err error }

func (e *cliReportedError) Error() string { return e.err.Error() }
func (e *cliReportedError) Unwrap() error { return e.err }

type cliOptions struct {
	transport    string
	allowLocalWS bool
	debug        bool
}

// Prepare and validate everything before constructing an engine or opening a socket.
func cliRequest(data []byte, options cliOptions) (core.Request, error) {
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
	switch options.transport {
	case "":
	case "auto":
		cfg.Transport.Preferred = core.PreferredICE
		cfg.Transport.AllowLegacy = true
	case "ice":
		cfg.Transport.Preferred = core.PreferredICE
		cfg.Transport.AllowLegacy = false
	case "ipv6":
		cfg.Transport.Preferred = core.PreferredIPv6
		cfg.Transport.AllowLegacy = false
	default:
		return core.Request{}, fmt.Errorf("transport 需要 auto、ice 或 ipv6")
	}
	if options.allowLocalWS {
		cfg.Transport.AllowInsecureSignal = true
	}
	request := core.Request{ServerURL: serverURL, Config: cfg}
	return request, request.Validate()
}

func run(configPath string, options cliOptions) error {
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
	restoreLibraryLogs := reporter.captureLibraryLogs()
	defer restoreLibraryLogs()
	reporter.startup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	engine := core.NewEngine(core.Options{})
	defer engine.Close()
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		reporter.follow(engine)
	}()
	err = engine.Start(request)
	if err == nil {
		err = engine.Wait(ctx)
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
