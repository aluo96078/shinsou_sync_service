package syncsvc

import "testing"

func TestJSONSafe(t *testing.T) {
	cases := map[string]bool{
		`"plain"`:                true,
		`"é \" \\ \n é"`:         true,
		`"😀"`:                    true,
		`"\u0000"`:               false,
		`"\ud800"`:               false,
		`"\ud800x"`:              false,
		`"\ud800A"`:              false,
		`"\udc00"`:               false,
		`{"a":["\\u0000"]}`:      true, // an escaped backslash followed by text, not an escape
		`{"a":"\u0000"}`:         false,
		`"\u00"`:                 false,
		`"\uZZZZ"`:               false,
		`123`:                    true,
		`"trailing backslash\\"`: true,
	}
	for in, want := range cases {
		if got := jsonSafe([]byte(in)); got != want {
			t.Errorf("jsonSafe(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestValidKey(t *testing.T) {
	uuid := "123e4567-e89b-42d3-a456-426614174000"
	for _, c := range []struct {
		kind, key string
		ok        bool
	}{
		{"library", "local:" + uuid, true},
		{"category", "default", true},
		{"setting", "a.b", true},
		{"setting", "A", false},
		{"unit", "anything", false}, // units are keyed by recomputation only
		{"bogus", "x", false},
	} {
		if got := validKey(c.kind, c.key); got != c.ok {
			t.Errorf("validKey(%s,%s) = %v", c.kind, c.key, got)
		}
	}
}
