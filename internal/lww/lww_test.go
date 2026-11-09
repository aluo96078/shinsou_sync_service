package lww

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/hlc"
)

type vectorFile struct {
	HLC struct {
		Valid   []string    `json:"valid"`
		Invalid []string    `json:"invalid"`
		Ordered [][2]string `json:"ordered"`
	} `json:"hlc"`
	Merge []struct {
		Name     string   `json:"name"`
		Now      int64    `json:"now"`
		Initial  *State   `json:"initial"`
		Pushes   []Push   `json:"pushes"`
		Results  []Result `json:"results"`
		Expect   State    `json:"expect"`
		AnyOrder bool     `json:"anyOrder"`
	} `json:"merge"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("../../api/vectors/lww.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Merge) == 0 {
		t.Fatal("vector file has no merge cases")
	}
	return v
}

func TestHLCVectors(t *testing.T) {
	v := loadVectors(t).HLC
	for _, s := range v.Valid {
		ts, err := hlc.Parse(s)
		if err != nil {
			t.Errorf("Parse(%q) failed: %v", s, err)
			continue
		}
		if ts.String() != s {
			t.Errorf("round trip %q -> %q", s, ts.String())
		}
	}
	for _, s := range v.Invalid {
		if _, err := hlc.Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted an invalid timestamp", s)
		}
	}
	for _, pair := range v.Ordered {
		a, errA := hlc.Parse(pair[0])
		b, errB := hlc.Parse(pair[1])
		if errA != nil || errB != nil {
			t.Fatalf("ordered pair %v does not parse", pair)
		}
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("want %s < %s", pair[0], pair[1])
		}
	}
}

func TestMergeVectors(t *testing.T) {
	for _, c := range loadVectors(t).Merge {
		t.Run(c.Name, func(t *testing.T) {
			if len(c.Results) != len(c.Pushes) {
				t.Fatalf("vector has %d pushes but %d results", len(c.Pushes), len(c.Results))
			}
			state := c.Initial
			for i, p := range c.Pushes {
				next, r := Merge(state, p, c.Now)
				if r != c.Results[i] {
					t.Errorf("push %d: result = %s, want %s", i, r, c.Results[i])
				}
				state = &next
			}
			assertState(t, *state, c.Expect)

			if c.AnyOrder {
				permute(len(c.Pushes), func(order []int) {
					s := c.Initial
					for _, i := range order {
						next, _ := Merge(s, c.Pushes[i], c.Now)
						s = &next
					}
					assertState(t, *s, c.Expect)
				})
			}
		})
	}
}

func TestMergeDoesNotMutateInput(t *testing.T) {
	s := &State{
		Fields: map[string]json.RawMessage{"x": json.RawMessage(`1`)},
		Clocks: map[string]string{"x": "1:0:a"},
	}
	_, r := Merge(s, Push{Deleted: &Register{Value: true, Clock: "2:0:a"}}, 10)
	if r != Applied {
		t.Fatalf("result = %s", r)
	}
	if _, ok := s.Fields["x"]; !ok || s.Deleted != nil || s.Cleared != "" {
		t.Fatalf("input state was mutated: %+v", s)
	}
}

func assertState(t *testing.T, got, want State) {
	t.Helper()
	if !reflect.DeepEqual(normalize(t, got), normalize(t, want)) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Errorf("state =\n  %s\nwant\n  %s", g, w)
	}
}

// normalize decodes field values so they compare as JSON values, and treats nil maps as empty.
func normalize(t *testing.T, s State) map[string]any {
	t.Helper()
	fields := map[string]any{}
	for k, raw := range s.Fields {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("field %s is not valid JSON: %v", k, err)
		}
		fields[k] = v
	}
	clocks := map[string]string{}
	for k, c := range s.Clocks {
		clocks[k] = c
	}
	out := map[string]any{"fields": fields, "clocks": clocks, "cleared": s.Cleared}
	if s.Deleted != nil {
		out["deleted"] = *s.Deleted
	}
	return out
}

func permute(n int, visit func([]int)) {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	var rec func(int)
	rec = func(k int) {
		if k == n {
			visit(append([]int(nil), idx...))
			return
		}
		for i := k; i < n; i++ {
			idx[k], idx[i] = idx[i], idx[k]
			rec(k + 1)
			idx[k], idx[i] = idx[i], idx[k]
		}
	}
	rec(0)
}
