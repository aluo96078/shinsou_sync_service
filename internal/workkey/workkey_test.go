package workkey

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

type vectorFile struct {
	Valid []struct {
		Name      string `json:"name"`
		Input     Work   `json:"input"`
		Canonical string `json:"canonical"`
		Hash      string `json:"hash"`
		Units     []struct {
			UnitRemoteID string `json:"unitRemoteId"`
			Canonical    string `json:"canonical"`
			Hash         string `json:"hash"`
		} `json:"units"`
	} `json:"valid"`
	Invalid []struct {
		Name         string  `json:"name"`
		Input        Work    `json:"input"`
		UnitRemoteID *string `json:"unitRemoteId"`
	} `json:"invalid"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("../../api/vectors/work-key.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Valid) == 0 || len(v.Invalid) == 0 {
		t.Fatal("vector file has no cases")
	}
	return v
}

func TestValidVectors(t *testing.T) {
	for _, c := range loadVectors(t).Valid {
		t.Run(c.Name, func(t *testing.T) {
			canonical, err := c.Input.Canonical()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if canonical != c.Canonical {
				t.Errorf("canonical = %q, want %q", canonical, c.Canonical)
			}
			hash, _ := c.Input.Hash()
			if hash != c.Hash {
				t.Errorf("hash = %s, want %s", hash, c.Hash)
			}
			for _, u := range c.Units {
				uc, err := c.Input.UnitCanonical(u.UnitRemoteID)
				if err != nil {
					t.Fatalf("unit %q: unexpected error: %v", u.UnitRemoteID, err)
				}
				if uc != u.Canonical {
					t.Errorf("unit canonical = %q, want %q", uc, u.Canonical)
				}
				uh, _ := c.Input.UnitHash(u.UnitRemoteID)
				if uh != u.Hash {
					t.Errorf("unit hash = %s, want %s", uh, u.Hash)
				}
			}
		})
	}
}

func TestInvalidVectors(t *testing.T) {
	for _, c := range loadVectors(t).Invalid {
		t.Run(c.Name, func(t *testing.T) {
			var err error
			if c.UnitRemoteID != nil {
				_, err = c.Input.UnitHash(*c.UnitRemoteID)
			} else {
				_, err = c.Input.Hash()
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestInvalidUTF8(t *testing.T) {
	w := Work{KeyVersion: 1, PackageID: "p", SourceID: "s", RemoteID: "a\xffb"}
	if _, err := w.Hash(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
