//go:build minecraftjni

// Thin JVM facade. All transport and Android network binding live in mobile/core.
package main

/*
#include <stdlib.h>
#include <stdint.h>
char *hole_android_call(char *operation, char *input);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"hole/mobile"
	"sync"
	"unsafe"
)

type instance struct {
	mu     sync.Mutex
	engine *mobile.Engine
}

var registry = struct {
	sync.Mutex
	next  uint64
	items map[uint64]*instance
}{items: map[uint64]*instance{}}

type androidBinding struct{}

func androidCall(op, input string) (string, error) {
	a, b := C.CString(op), C.CString(input)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(b))
	out := C.hole_android_call(a, b)
	if out == nil {
		return "", errors.New("FCL Android 网络接口调用失败")
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out), nil
}
func (androidBinding) InterfacesJSON() (string, error) { return androidCall("interfaces", "") }
func (androidBinding) NetworkJSON() (string, error)    { return androidCall("network", "") }
func (androidBinding) BindSocket(fd int64, handle string) error {
	b, _ := json.Marshal(map[string]any{"fd": fd, "handle": handle})
	_, err := androidCall("bind", string(b))
	return err
}
func (androidBinding) LookupIP(host, handle string) (string, error) {
	b, _ := json.Marshal(map[string]string{"host": host, "handle": handle})
	return androidCall("lookup", string(b))
}

//export hole_mc_open
func hole_mc_open(android C.int) C.uint64_t {
	var e *mobile.Engine
	if android != 0 {
		e, _ = mobile.NewEngineWithNetworkBinding(nil, androidBinding{})
	} else {
		e = mobile.NewEngine(nil)
	}
	if e == nil {
		return 0
	}
	registry.Lock()
	defer registry.Unlock()
	registry.next++
	registry.items[registry.next] = &instance{engine: e}
	return C.uint64_t(registry.next)
}

func result(value string, err error) *C.char {
	response := struct {
		Value string `json:"value"`
		Error string `json:"error,omitempty"`
	}{Value: value}
	if err != nil {
		response.Error = err.Error()
	}
	b, _ := json.Marshal(response)
	return C.CString(string(b))
}

//export hole_mc_call
func hole_mc_call(id C.uint64_t, op *C.char, input *C.char, length C.int) (out *C.char) {
	defer func() {
		if recover() != nil {
			out = result("", errors.New("native_bridge_failure"))
		}
	}()
	if length < 0 || length > 128*1024 {
		return result("", errors.New("request_too_large"))
	}
	registry.Lock()
	entry := registry.items[uint64(id)]
	registry.Unlock()
	if entry == nil {
		return result("", errors.New("核心已关闭"))
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.engine == nil {
		return result("", errors.New("核心已关闭"))
	}
	e := entry.engine
	var err error
	value := ""
	switch C.GoString(op) {
	case "start":
		err = e.Start(C.GoStringN(input, length))
	case "apply":
		err = e.ApplyConfig(C.GoStringN(input, length))
	case "stop":
		err = e.Stop()
	case "snapshot":
		value = e.SnapshotJSON()
	case "set_voice_muted":
		var inputValue struct {
			Muted bool `json:"muted"`
		}
		err = json.Unmarshal([]byte(C.GoStringN(input, length)), &inputValue)
		if err == nil {
			err = e.SetVoiceMuted(inputValue.Muted)
		}
	case "audio_push":
		var inputValue struct {
			Sequence int64  `json:"sequence"`
			PCM      []byte `json:"pcm"`
		}
		err = json.Unmarshal([]byte(C.GoStringN(input, length)), &inputValue)
		if err == nil {
			if inputValue.Sequence < 0 || inputValue.Sequence >= 1<<52 {
				err = errors.New("语音序号无效")
			} else {
				err = e.PushVoicePCM(inputValue.Sequence, inputValue.Sequence*960, inputValue.PCM)
			}
		}
	case "audio_pull":
		data, _ := json.Marshal(map[string][]byte{"pcm": e.PullVoicePCM(1)})
		value = string(data)
	case "android_audio":
		value, err = androidCall("audio", C.GoStringN(input, length))
	case "platform_network":
		value, err = androidCall("network", "")
	case "network":
		err = e.NetworkChanged(C.GoStringN(input, length))
	default:
		err = errors.New("未知桥接操作")
	}
	return result(value, err)
}

//export hole_mc_close
func hole_mc_close(id C.uint64_t) {
	registry.Lock()
	entry := registry.items[uint64(id)]
	delete(registry.items, uint64(id))
	registry.Unlock()
	if entry == nil {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.engine != nil {
		_, _ = androidCall("audio", `{"op":"stop"}`)
		_ = entry.engine.Close()
		entry.engine = nil
	}
}
func main() {}
