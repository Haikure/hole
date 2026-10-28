package core

import (
	"time"
)

func quicRTTMilliseconds(rtt time.Duration) int64 {
	if rtt <= 0 {
		return 0
	}
	nanos := int64(rtt)
	return max(1, (nanos+int64(time.Millisecond)-1)/int64(time.Millisecond))
}
