package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hole/core"
)

func writeCLIConfig(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCLIConfigReloadValidationOverridesAndRetry(t *testing.T) {
	r, output := testReporter(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	options := cliOptions{transport: "ice", allowLocalWS: true}
	data := "server_url: ws://127.0.0.1/ws\n" + cliFixtureConfig
	initial, err := cliRequest([]byte(data), options)
	if err != nil {
		t.Fatal(err)
	}
	r.request = initial
	var applied []core.Request
	var applyErr error
	reloader := cliConfigReloader{path: path, options: options, current: initial, reporter: r, apply: func(next core.Request) error {
		// Also checks that ApplyConfig can synchronously emit library logs.
		_, _ = (&cliLibraryLog{reporter: r}).Write([]byte(next.Config.Token))
		if applyErr != nil {
			return applyErr
		}
		applied = append(applied, next)
		return nil
	}}
	for _, invalid := range []string{"", "room: [", data + "session_timeout: PRIVATE_INVALID_VALUE\n", strings.Replace(data, "token: PRIVATE_ROOM_TOKEN", "token: ''", 1)} {
		writeCLIConfig(t, path, invalid)
		reloader.reload()
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reloader.reload()
	if len(applied) != 0 || !reflect.DeepEqual(reloader.current, initial) || !reflect.DeepEqual(r.request, initial) {
		t.Fatal("invalid/missing file changed the running configuration")
	}
	if !strings.Contains(output.String(), "继续使用原配置") || strings.Contains(output.String(), "PRIVATE_INVALID_VALUE") {
		t.Fatal("invalid configuration was not reported safely", output.String())
	}
	// Comments and an overridden transport value do not change effective config.
	writeCLIConfig(t, path, "# comment\n"+strings.Replace(data, "preferred: ice", "preferred: ipv6", 1))
	reloader.reload()
	if len(applied) != 0 {
		t.Fatal("identical effective config triggered an update")
	}
	next := strings.Replace(data, "PRIVATE_ROOM_TOKEN", "PRIVATE_NEXT_TOKEN", 1) + "session_timeout: 45s\n"
	writeCLIConfig(t, path, next)
	applyErr = errors.New("rejected")
	reloader.reload()
	if !reflect.DeepEqual(reloader.current, initial) || !reflect.DeepEqual(r.request, initial) {
		t.Fatal("failed apply replaced the last accepted configuration")
	}
	applyErr = nil
	reloader.reload()
	if len(applied) != 1 || applied[0].Config.SessionTimeout != 45*time.Second || applied[0].Config.Transport.AllowLegacy || !applied[0].Config.Transport.AllowInsecureSignal {
		t.Fatalf("new config or CLI overrides were lost: %+v", applied)
	}
	if !reflect.DeepEqual(r.request, applied[0]) {
		t.Fatal("reporter retained stale mapping/identity information")
	}
	reloader.reload()
	if len(applied) != 1 {
		t.Fatal("repeated save applied twice")
	}
	r.info("old=%s new=%s", initial.Config.Token, applied[0].Config.Token)
	if strings.Contains(output.String(), "PRIVATE_ROOM_TOKEN") || strings.Contains(output.String(), "PRIVATE_NEXT_TOKEN") {
		t.Fatal("reload leaked old or new credentials", output.String())
	}
}

func TestCLIConfigWatcherSavesAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := "server_url: wss://fixture.invalid/ws\n" + cliFixtureConfig
	writeCLIConfig(t, path, data)
	w, err := newCLIConfigWatcher(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r, _ := testReporter(t)
	initial, err := cliRequest([]byte(data), cliOptions{})
	if err != nil {
		t.Fatal(err)
	}
	applied := make(chan core.Request, 16)
	observed := make(chan core.Request, 16)
	watchErrors := make(chan error, 16)
	reloader := cliConfigReloader{path: path, current: initial, reporter: r, apply: func(next core.Request) error {
		applied <- next
		return nil
	}}
	// Saving between initial read and starting the loop must not be missed.
	writeCLIConfig(t, path, data+"session_timeout: 31s\n")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.run(ctx, func() {
			reloader.reload()
			observed <- reloader.current
		}, func(err error) { watchErrors <- err })
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitReload := func(timeout time.Duration, changed bool) {
		t.Helper()
		select {
		case got := <-observed:
			if got.Config.SessionTimeout != timeout {
				t.Fatalf("timeout = %s, want %s", got.Config.SessionTimeout, timeout)
			}
		case err := <-watchErrors:
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("file save was not reloaded")
		}
		if changed {
			select {
			case <-applied:
			default:
				t.Fatal("valid change did not reach ApplyConfig")
			}
		} else if len(applied) != 0 {
			t.Fatal("invalid or unchanged config reached ApplyConfig")
		}
	}
	waitReload(31*time.Second, true)
	for _, timeout := range []string{"32s", "33s", "34s"} {
		writeCLIConfig(t, path, data+"session_timeout: "+timeout+"\n")
	}
	waitReload(34*time.Second, true)
	// Replace twice to prove the watch survives the old inode disappearing.
	for _, timeout := range []string{"35s", "36s"} {
		tmp := filepath.Join(dir, "save.tmp")
		writeCLIConfig(t, tmp, data+"session_timeout: "+timeout+"\n")
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
		want, _ := time.ParseDuration(timeout)
		waitReload(want, true)
	}
	writeCLIConfig(t, path, "room: [")
	waitReload(36*time.Second, false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitReload(36*time.Second, false)
	writeCLIConfig(t, path, data+"session_timeout: 37s\n")
	waitReload(37*time.Second, true)
	writeCLIConfig(t, path, "# unchanged\n"+data+"session_timeout: 37s\n")
	waitReload(37*time.Second, false)
	writeCLIConfig(t, filepath.Join(dir, "unrelated.yaml"), "ignore")
	select {
	case <-observed:
		t.Fatal("unrelated file or duplicate event triggered reload")
	case <-time.After(2 * cliConfigDebounce):
	}
	writeCLIConfig(t, path, data+"session_timeout: 38s\n")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop on cancellation")
	}
	if len(observed) != 0 || len(applied) != 0 {
		t.Fatal("pending save was applied after cancellation")
	}
}

func TestCLIReporterConcurrentReloadAndLibraryLogs(t *testing.T) {
	r, output := testReporter(t)
	next := r.request
	next.Config.Token = "PRIVATE_RELOADED_TOKEN"
	next.ServerURL = "wss://other.invalid/ws?secret=PRIVATE_RELOADED_URL"
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			r.followEvent(core.Event{Kind: "config", State: "applied"}, core.Snapshot{})
		}
	})
	wg.Go(func() {
		for range 100 {
			r.mu.Lock()
			r.protectRequest(next)
			r.request = next
			r.mu.Unlock()
			_, _ = (&cliLibraryLog{reporter: r}).Write([]byte(next.Config.Token + " " + next.ServerURL))
		}
	})
	wg.Wait()
	if strings.Contains(output.String(), "PRIVATE_RELOADED") {
		t.Fatal("concurrent library logging leaked reloaded credentials")
	}
}
