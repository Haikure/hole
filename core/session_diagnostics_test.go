package core

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
)

func TestServiceSessionErrorsKeepTheirRealCause(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errors.New("session_expired"), "session_expired"},
		{errors.New("session_limit"), "session_limit"},
		{context.Canceled, "session_canceled"},
		{context.DeadlineExceeded, "service_timeout"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "service_refused"},
		{errors.New("platform-specific socket error"), "service_unavailable"},
	} {
		fault := serviceSessionFault(tc.err, "127.0.0.1:12345", false)
		if fault.Code != tc.code || !strings.Contains(fault.Message, tc.err.Error()) || !strings.Contains(fault.Message, "127.0.0.1:12345") {
			t.Fatal(fault)
		}
	}
	if f := serviceSessionFault(errors.New("session_expired"), "TARGET", true); !strings.Contains(f.Message, "恢复应用会话") {
		t.Fatal(f)
	}
	if f := serviceSessionFault(errors.New(strings.Repeat("x", 10000)), "TARGET", false); len(f.Message) > 2100 {
		t.Fatal("unbounded diagnostic")
	}
}
