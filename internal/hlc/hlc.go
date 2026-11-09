// Package hlc parses, formats and orders Hybrid Logical Clock timestamps as defined in
// docs/spec/sync-semantics.md section 1.
package hlc

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Limits from the spec.
const (
	MaxMillis  = 1<<53 - 1
	MaxCounter = 1<<31 - 1
)

// ErrInvalid is returned for any malformed timestamp.
var ErrInvalid = errors.New("invalid hlc timestamp")

var pattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,15}):(0|[1-9][0-9]{0,9}):([A-Za-z0-9_-]{1,64})$`)

// Timestamp is one HLC value. The zero value is not a valid timestamp.
type Timestamp struct {
	Millis  int64
	Counter int32
	Device  string
}

// Parse accepts only the canonical form, so equal timestamps have equal strings.
func Parse(s string) (Timestamp, error) {
	m := pattern.FindStringSubmatch(s)
	if m == nil {
		return Timestamp{}, ErrInvalid
	}
	millis, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || millis > MaxMillis {
		return Timestamp{}, ErrInvalid
	}
	counter, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil || counter > MaxCounter {
		return Timestamp{}, ErrInvalid
	}
	return Timestamp{Millis: millis, Counter: int32(counter), Device: m[3]}, nil
}

// String returns the canonical form.
func (t Timestamp) String() string {
	return strconv.FormatInt(t.Millis, 10) + ":" + strconv.FormatInt(int64(t.Counter), 10) + ":" + t.Device
}

// Compare orders by millis, then counter, then device id bytes.
func (t Timestamp) Compare(o Timestamp) int {
	switch {
	case t.Millis != o.Millis:
		if t.Millis < o.Millis {
			return -1
		}
		return 1
	case t.Counter != o.Counter:
		if t.Counter < o.Counter {
			return -1
		}
		return 1
	default:
		return strings.Compare(t.Device, o.Device)
	}
}
