package syncsvc

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/workkey"
	"net/http"
)

var (
	hashRe    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uuidRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	settingRe = regexp.MustCompile(`^[a-z][a-zA-Z0-9.]{0,127}$`)
)

var kinds = map[string]bool{
	"library": true, "unit": true, "progress": true, "history": true,
	"category": true, "repository": true, "setting": true,
}

// workKeyed kinds use a work hash or local:<uuid> as their key.
func workKeyed(kind string) bool { return kind == "library" || kind == "progress" || kind == "history" }

// validKey implements the key formats of docs/spec/records.md section 1 for every kind except
// unit, whose key is recomputed from the work.
func validKey(kind, key string) bool {
	switch kind {
	case "library", "progress", "history":
		return hashRe.MatchString(key) || (strings.HasPrefix(key, "local:") && uuidRe.MatchString(key[len("local:"):]))
	case "category":
		return key == "default" || uuidRe.MatchString(key)
	case "repository":
		return key != "" && len(key) <= 2048 && utf8.ValidString(key) && !hasNUL(key) &&
			strings.TrimFunc(key, unicode.IsSpace) != ""
	case "setting":
		return settingRe.MatchString(key)
	}
	return false
}

func hasNUL(s string) bool { return strings.IndexByte(s, 0) >= 0 }

// validateWork checks a descriptor and recomputes its hash.
func validateWork(w Work) *apierr.Error {
	id := workkey.Work{KeyVersion: w.KeyVersion, PackageID: w.PackageID, SourceID: w.SourceID, RemoteID: w.RemoteID}
	hash, err := id.Hash()
	if err != nil {
		return apierr.New(http.StatusUnprocessableEntity, "invalid_work_key", err.Error())
	}
	if hash != w.Hash {
		return apierr.New(http.StatusUnprocessableEntity, "work_hash_mismatch", "work hash does not match its key components")
	}
	if n := utf8.RuneCountInString(w.Title); n < 1 || n > 512 || strings.TrimFunc(w.Title, unicode.IsSpace) == "" || hasNUL(w.Title) {
		return apierr.Validation("work title must be 1-512 characters")
	}
	if w.ContentKind != "manga" && w.ContentKind != "novel" {
		return apierr.Validation("work contentKind must be manga or novel")
	}
	for name, p := range map[string]struct {
		v   *string
		max int
	}{"author": {w.Author, 512}, "language": {w.Language, 35}, "sourceName": {w.SourceName, 128}} {
		if p.v != nil && (utf8.RuneCountInString(*p.v) > p.max || hasNUL(*p.v)) {
			return apierr.Validation("work " + name + " is too long or invalid")
		}
	}
	if w.CoverURL != nil && !validURL(*w.CoverURL, true) {
		return apierr.Validation("work coverUrl must be an https URL of at most 2048 bytes")
	}
	if w.RepositoryURL != nil && !validURL(*w.RepositoryURL, false) {
		return apierr.Validation("work repositoryUrl must be an http(s) URL of at most 2048 bytes")
	}
	return nil
}

func validURL(s string, httpsOnly bool) bool {
	if len(s) > 2048 || hasNUL(s) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || (!httpsOnly && u.Scheme == "http")
}

// jsonSafe reports whether a raw JSON value can be stored in a PostgreSQL jsonb column. jsonb
// rejects the \u0000 escape and unpaired surrogate escapes, which would otherwise surface as a
// database error for the whole batch.
func jsonSafe(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		cp, ok := hex4(raw, i+2)
		if !ok || cp == 0 {
			return false
		}
		i += 5
		switch {
		case cp >= 0xD800 && cp <= 0xDBFF:
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			lo, ok := hex4(raw, i+3)
			if !ok || lo < 0xDC00 || lo > 0xDFFF {
				return false
			}
			i += 6
		case cp >= 0xDC00 && cp <= 0xDFFF:
			return false
		}
	}
	return true
}

func hex4(b []byte, at int) (int, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	v := 0
	for _, c := range b[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | int(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | int(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | int(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}
