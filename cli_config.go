package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"hole/core"

	"github.com/fsnotify/fsnotify"
)

const cliConfigDebounce = 200 * time.Millisecond

type cliConfigWatcher struct {
	*fsnotify.Watcher
	path string
}

func newCLIConfigWatcher(path string) (*cliConfigWatcher, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建配置监听失败: %w", err)
	}
	// Editors often replace the file by rename. A directory watch survives it,
	// and also survives temporary deletion followed by recreation of the file.
	if err := w.Add(filepath.Dir(path)); err != nil {
		w.Close()
		return nil, fmt.Errorf("监听配置目录失败: %w", err)
	}
	return &cliConfigWatcher{Watcher: w, path: path}, nil
}

func (w *cliConfigWatcher) run(ctx context.Context, reload func(), reportError func(error)) {
	timer := time.NewTimer(cliConfigDebounce)
	defer timer.Stop()
	// Recheck once after startup, including changes queued before this loop.
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) == w.path && event.Has(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) {
				timer.Reset(cliConfigDebounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			reportError(err)
			// An overflow may have dropped the save event. Reconcile from disk.
			timer.Reset(cliConfigDebounce)
		case <-timer.C:
			if ctx.Err() != nil {
				return
			}
			reload()
		}
	}
}

// Only the watcher goroutine owns current; the engine owns connection lifetime.
type cliConfigReloader struct {
	path     string
	options  cliOptions
	current  core.Request
	reporter *cliReporter
	apply    func(core.Request) error
}

func (r *cliConfigReloader) reload() {
	data, err := os.ReadFile(r.path)
	if err != nil {
		r.warn("配置重载失败: 无法读取配置文件，继续使用原配置")
		return
	}
	next, err := cliRequest(data, r.options)
	if err != nil {
		// Parser errors can echo values from an invalid document, whose secrets
		// we cannot reliably extract. Never log its contents, even in debug mode.
		r.warn("配置重载失败: 配置格式或字段无效，继续使用原配置")
		return
	}
	if reflect.DeepEqual(r.current, next) {
		return
	}
	r.reporter.mu.Lock()
	r.reporter.protectRequest(next)
	r.reporter.mu.Unlock()
	// ApplyConfig may emit library logs synchronously; do not hold reporter.mu.
	if err := r.apply(next); err != nil {
		r.warn("配置重载失败: 引擎未接受新配置，继续使用原配置")
		return
	}
	r.current = next
	r.reporter.mu.Lock()
	defer r.reporter.mu.Unlock()
	r.reporter.request = next
	delete(r.reporter.messages, "config")
	r.reporter.info("配置已重载，正在应用")
}

func (r *cliConfigReloader) warn(message string) {
	r.reporter.mu.Lock()
	defer r.reporter.mu.Unlock()
	r.reporter.warn("%s", message)
}
