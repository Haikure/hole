//go:build desktopjni

// Package main exports the shared mobile facade through a small desktop C ABI.
// No transport, session or identity logic belongs in this package.
package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
	"unsafe"

	"hole/mobile"
)

type instance struct {
	mu     sync.Mutex
	engine *mobile.Engine
}

var instances = struct {
	sync.Mutex
	next  uint64
	items map[uint64]*instance
}{items: make(map[uint64]*instance)}

func lookup(id uint64) *instance {
	instances.Lock()
	defer instances.Unlock()
	return instances.items[id]
}

//export hole_open
func hole_open() C.uint64_t {
	instances.Lock()
	defer instances.Unlock()
	instances.next++
	instances.items[instances.next] = &instance{engine: mobile.NewEngine(nil)}
	return C.uint64_t(instances.next)
}

func response(value string, err error) *C.char {
	result := struct {
		Value string `json:"value"`
		Error string `json:"error,omitempty"`
	}{Value: value}
	if err != nil {
		result.Error = err.Error()
	}
	data, _ := json.Marshal(result)
	return C.CString(string(data))
}

//export hole_call
func hole_call(id C.uint64_t, operation *C.char, input *C.char, length C.int) (output *C.char) {
	// Never unwind a Go panic across a JNI boundary.
	defer func() {
		if recover() != nil {
			output = response("", errors.New("native_bridge_failure"))
		}
	}()
	if length < 0 || length > 128*1024 {
		return response("", errors.New("请求超过 128 KiB"))
	}
	text := C.GoStringN(input, length)
	if !utf8.ValidString(text) {
		return response("", errors.New("请求不是 UTF-8"))
	}
	op := C.GoString(operation)
	// Pure configuration conversion needs no engine and never starts networking.
	switch op {
	case "decode":
		value, err := mobile.DecodeCLIConfig(text)
		return response(value, err)
	case "encode", "encode_secrets":
		value, err := mobile.EncodeCLIConfig(text, op == "encode_secrets")
		return response(value, err)
	}
	entry := lookup(uint64(id))
	if entry == nil {
		return response("", errors.New("核心已关闭"))
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	e := entry.engine
	if e == nil {
		return response("", errors.New("核心已关闭"))
	}
	var err error
	var value string
	switch op {
	case "version":
		value = fmt.Sprint(mobile.Version())
	case "start":
		err = e.Start(text)
	case "stop":
		err = e.Stop()
	case "apply":
		err = e.ApplyConfig(text)
	case "snapshot":
		value = e.SnapshotJSON()
	case "network":
		err = e.NetworkChanged(text)
	case "renominate":
		err = e.RenominateTransports()
	case "nat_start":
		err = e.StartNATProbe(text)
	case "nat_cancel":
		err = e.CancelNATProbe()
	case "mute":
		if text != "true" && text != "false" {
			err = errors.New("静音状态无效")
		} else {
			err = e.SetVoiceMuted(text == "true")
		}
	case "punch":
		var request struct {
			Transport string `json:"transport"`
			After     string `json:"after"`
		}
		err = json.Unmarshal([]byte(text), &request)
		if err == nil {
			value = e.NATPunchProcessJSON(request.Transport, request.After)
		}
	default:
		err = errors.New("未知原生桥接操作")
	}
	return response(value, err)
}

//export hole_close
func hole_close(id C.uint64_t) {
	instances.Lock()
	entry := instances.items[uint64(id)]
	delete(instances.items, uint64(id))
	instances.Unlock()
	if entry == nil {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	_ = entry.engine.Close()
	entry.engine = nil
}

//export hole_push
func hole_push(id C.uint64_t, sequence C.int64_t, timestamp C.int64_t, data unsafe.Pointer, length C.int) C.int {
	if length <= 0 || length > 4*1920 || length%1920 != 0 {
		return -1
	}
	entry := lookup(uint64(id))
	if entry == nil {
		return -1
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.engine == nil {
		return -1
	}
	if err := entry.engine.PushVoicePCM(int64(sequence), int64(timestamp), C.GoBytes(data, length)); err != nil {
		var fault struct {
			Code string `json:"code"`
		}
		if json.Unmarshal([]byte(err.Error()), &fault) == nil && (fault.Code == "voice_unavailable" || fault.Code == "voice_disabled") {
			return 1 // Drop this frame while the shared core starts or changes scope.
		}
		return -1
	}
	return 0
}

//export hole_pull
func hole_pull(id C.uint64_t, data unsafe.Pointer, capacity C.int) C.int {
	if capacity != 4*1920 {
		return -1
	}
	entry := lookup(uint64(id))
	if entry == nil {
		return -1
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.engine == nil {
		return -1
	}
	pcm := entry.engine.PullVoicePCM(4)
	copy(unsafe.Slice((*byte)(data), int(capacity)), pcm)
	return C.int(len(pcm))
}

func main() {}
