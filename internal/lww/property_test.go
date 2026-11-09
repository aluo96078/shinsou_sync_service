package lww

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
)

// TestConvergenceProperty checks section 4's claim: with distinct clocks, every delivery order,
// with duplicates, ends in the same state.
func TestConvergenceProperty(t *testing.T) {
	const now = int64(1_000_000)
	fields := []string{"read", "pinned", "title", "order"}
	devices := []string{"a", "b", "c"}

	for seed := uint64(0); seed < 500; seed++ {
		rng := rand.New(rand.NewPCG(seed, 7))
		var pushes []Push
		tick := 0
		for range 2 + rng.IntN(8) {
			dev := devices[rng.IntN(len(devices))]
			// Unique clocks: one global counter, random device.
			next := func() string { tick++; return fmt.Sprintf("%d:%d:%s", 1000+rng.IntN(5), tick, dev) }
			p := Push{}
			if rng.IntN(4) == 0 {
				p.Deleted = &Register{Value: rng.IntN(2) == 0, Clock: next()}
			}
			if p.Deleted == nil || rng.IntN(2) == 0 {
				p.Fields = map[string]json.RawMessage{}
				p.Clocks = map[string]string{}
				for _, f := range fields {
					if rng.IntN(2) == 0 {
						p.Fields[f] = json.RawMessage(fmt.Sprintf("%d", rng.IntN(100)))
						p.Clocks[f] = next()
					}
				}
				if len(p.Fields) == 0 && p.Deleted == nil {
					p.Fields[fields[0]] = json.RawMessage(`0`)
					p.Clocks[fields[0]] = next()
				}
			}
			pushes = append(pushes, p)
		}

		apply := func(order []Push) State {
			var s *State
			for _, p := range order {
				next, r := Merge(s, p, now)
				if r.Rejected() {
					t.Fatalf("seed %d: unexpected rejection %s", seed, r)
				}
				s = &next
			}
			return *s
		}
		want := apply(pushes)
		for range 20 {
			shuffled := append([]Push(nil), pushes...)
			// Duplicate a few pushes to exercise idempotence.
			for range rng.IntN(3) {
				shuffled = append(shuffled, pushes[rng.IntN(len(pushes))])
			}
			rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			assertState(t, apply(shuffled), want)
		}
	}
}
