// Package workkey is the reference implementation of docs/spec/work-key.md.
//
// Identities are used byte-for-byte: nothing is trimmed, case-folded, Unicode-normalized or
// URL-rewritten. Any change to the canonical form must bump the key version.
package workkey

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CurrentKeyVersion is the only key version this implementation accepts.
const CurrentKeyVersion = 1

// UTF-8 byte limits for each component.
const (
	MaxPackageIDBytes = 128
	MaxSourceIDBytes  = 128
	MaxRemoteIDBytes  = 1024
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("invalid work key")

// Work identifies one publication on one source.
type Work struct {
	KeyVersion int    `json:"keyVersion"`
	PackageID  string `json:"packageId"`
	SourceID   string `json:"sourceId"`
	RemoteID   string `json:"remoteId"`
}

// Validate checks every rule in section 3 of the spec.
func (w Work) Validate() error {
	if w.KeyVersion != CurrentKeyVersion {
		return fmt.Errorf("%w: unsupported key version %d", ErrInvalid, w.KeyVersion)
	}
	if err := validateComponent("packageId", w.PackageID, MaxPackageIDBytes); err != nil {
		return err
	}
	if err := validateComponent("sourceId", w.SourceID, MaxSourceIDBytes); err != nil {
		return err
	}
	return validateComponent("remoteId", w.RemoteID, MaxRemoteIDBytes)
}

// Canonical returns the length-prefixed canonical string.
func (w Work) Canonical() (string, error) {
	if err := w.Validate(); err != nil {
		return "", err
	}
	return "w1|" + lp(w.PackageID) + lp(w.SourceID) + lp(w.RemoteID), nil
}

// Hash returns the lowercase hex SHA-256 of the canonical string.
func (w Work) Hash() (string, error) {
	c, err := w.Canonical()
	if err != nil {
		return "", err
	}
	return hashHex(c), nil
}

// UnitCanonical returns the canonical string of a unit (chapter) inside this work.
func (w Work) UnitCanonical(unitRemoteID string) (string, error) {
	if err := w.Validate(); err != nil {
		return "", err
	}
	if err := validateComponent("unitRemoteId", unitRemoteID, MaxRemoteIDBytes); err != nil {
		return "", err
	}
	return "u1|" + lp(w.PackageID) + lp(w.SourceID) + lp(w.RemoteID) + lp(unitRemoteID), nil
}

// UnitHash returns the lowercase hex SHA-256 of the unit canonical string.
func (w Work) UnitHash(unitRemoteID string) (string, error) {
	c, err := w.UnitCanonical(unitRemoteID)
	if err != nil {
		return "", err
	}
	return hashHex(c), nil
}

func validateComponent(name, value string, maxBytes int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalid, name)
	}
	if strings.TrimFunc(value, unicode.IsSpace) == "" {
		return fmt.Errorf("%w: %s is blank", ErrInvalid, name)
	}
	for _, r := range value {
		if unicode.Is(unicode.Cc, r) {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalid, name)
		}
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalid, name, maxBytes)
	}
	return nil
}

func lp(s string) string { return strconv.Itoa(len(s)) + ":" + s }

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
