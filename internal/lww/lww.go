// Package lww is the reference implementation of the record merge rules in
// docs/spec/sync-semantics.md section 4.
package lww

import (
	"bytes"
	"encoding/json"
	"maps"
	"regexp"

	"github.com/aluo96078/shinsou_sync_service/internal/hlc"
)

// Limits from the spec.
const (
	MaxFields      = 64
	MaxFieldsBytes = 16 * 1024
	MaxClockAhead  = 5 * 60 * 1000 // milliseconds
)

// Result of merging one pushed record.
type Result string

const (
	Applied       Result = "applied"
	Unchanged     Result = "unchanged"
	MissingClock  Result = "rejected:missing_clock"
	InvalidClock  Result = "rejected:invalid_clock"
	InvalidRecord Result = "rejected:invalid_record"
	ClockSkew     Result = "rejected:clock_skew"
)

// Rejected reports whether the record was refused without touching the state.
func (r Result) Rejected() bool { return r != Applied && r != Unchanged }

var fieldName = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,63}$`)

// Register is a single LWW boolean register.
type Register struct {
	Value bool   `json:"value"`
	Clock string `json:"clock"`
}

// State is the stored state of one record.
type State struct {
	Fields  map[string]json.RawMessage `json:"fields"`
	Clocks  map[string]string          `json:"clocks"`
	Deleted *Register                  `json:"deleted,omitempty"`
	Cleared string                     `json:"cleared,omitempty"`
}

// Push is what a client sends for one record.
type Push struct {
	Fields  map[string]json.RawMessage `json:"fields,omitempty"`
	Clocks  map[string]string          `json:"clocks,omitempty"`
	Deleted *Register                  `json:"deleted,omitempty"`
}

// Merge applies p to s. s may be nil for a record that does not exist yet. nowMillis is the
// server's wall clock. The returned state is a new value; s is never modified.
func Merge(s *State, p Push, nowMillis int64) (State, Result) {
	cur := clone(s)
	if r := validate(p, nowMillis); r != "" {
		return cur, r
	}
	next := clone(&cur)

	if p.Deleted != nil {
		in, _ := hlc.Parse(p.Deleted.Clock)
		if next.Deleted == nil || in.Compare(mustParse(next.Deleted.Clock)) > 0 {
			d := *p.Deleted
			next.Deleted = &d
		}
		if p.Deleted.Value && (next.Cleared == "" || in.Compare(mustParse(next.Cleared)) > 0) {
			next.Cleared = p.Deleted.Clock
		}
	}

	for f, v := range p.Fields {
		in := mustParse(p.Clocks[f])
		old, ok := next.Clocks[f]
		if !ok || in.Compare(mustParse(old)) > 0 {
			next.Fields[f] = v
			next.Clocks[f] = p.Clocks[f]
		}
	}

	if next.Cleared != "" {
		cleared := mustParse(next.Cleared)
		for f, c := range next.Clocks {
			if mustParse(c).Compare(cleared) <= 0 {
				delete(next.Fields, f)
				delete(next.Clocks, f)
			}
		}
	}

	if len(next.Fields) > MaxFields || encodedSize(next.Fields) > MaxFieldsBytes {
		return cur, InvalidRecord
	}
	if equal(cur, next) {
		return cur, Unchanged
	}
	return next, Applied
}

func validate(p Push, nowMillis int64) Result {
	if len(p.Fields) == 0 && p.Deleted == nil {
		return InvalidRecord
	}
	if len(p.Fields) > MaxFields || encodedSize(p.Fields) > MaxFieldsBytes {
		return InvalidRecord
	}
	for f := range p.Fields {
		if !fieldName.MatchString(f) {
			return InvalidRecord
		}
		if _, ok := p.Clocks[f]; !ok {
			return MissingClock
		}
	}
	for f := range p.Clocks {
		if _, ok := p.Fields[f]; !ok {
			return InvalidRecord
		}
	}
	var clocks []string
	for _, c := range p.Clocks {
		clocks = append(clocks, c)
	}
	if p.Deleted != nil {
		clocks = append(clocks, p.Deleted.Clock)
	}
	skew := false
	for _, c := range clocks {
		t, err := hlc.Parse(c)
		if err != nil {
			return InvalidClock
		}
		if t.Millis > nowMillis+MaxClockAhead {
			skew = true
		}
	}
	if skew {
		return ClockSkew
	}
	return ""
}

func clone(s *State) State {
	out := State{Fields: map[string]json.RawMessage{}, Clocks: map[string]string{}}
	if s == nil {
		return out
	}
	maps.Copy(out.Fields, s.Fields)
	maps.Copy(out.Clocks, s.Clocks)
	if s.Deleted != nil {
		d := *s.Deleted
		out.Deleted = &d
	}
	out.Cleared = s.Cleared
	return out
}

func equal(a, b State) bool {
	if a.Cleared != b.Cleared || !maps.Equal(a.Clocks, b.Clocks) {
		return false
	}
	if (a.Deleted == nil) != (b.Deleted == nil) || (a.Deleted != nil && *a.Deleted != *b.Deleted) {
		return false
	}
	return maps.EqualFunc(a.Fields, b.Fields, func(x, y json.RawMessage) bool { return bytes.Equal(x, y) })
}

func encodedSize(fields map[string]json.RawMessage) int {
	if len(fields) == 0 {
		return 0
	}
	b, _ := json.Marshal(fields)
	return len(b)
}

func mustParse(s string) hlc.Timestamp {
	t, err := hlc.Parse(s)
	if err != nil {
		panic("lww: stored clock is invalid: " + s)
	}
	return t
}
