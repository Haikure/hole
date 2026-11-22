package core

import (
	"regexp"
	"strconv"
)

const roomMinimumVersion = "1.1.0"

var softwareVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)([-+][0-9A-Za-z.+-]+)?$`)

// Feature gates use the numeric release version. Development builds advertise
// their actual release baseline with a suffix; the wire handshake still checks
// the message format independently.
func softwareVersionAtLeast(value, minimum string) bool {
	parse := func(s string) ([3]uint64, bool) {
		var out [3]uint64
		if len(s) > 128 {
			return out, false
		}
		parts := softwareVersionPattern.FindStringSubmatch(s)
		if len(parts) < 4 {
			return out, false
		}
		for i, part := range parts[1:4] {
			if part == "" || (len(part) > 1 && part[0] == '0') {
				return out, false
			}
			n, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return out, false
			}
			out[i] = n
		}
		return out, true
	}
	a, ok := parse(value)
	b, valid := parse(minimum)
	if !ok || !valid {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func modernSoftware(version string) bool {
	return softwareVersionAtLeast(version, roomMinimumVersion)
}
