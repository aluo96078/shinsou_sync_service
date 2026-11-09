package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Hasher hashes passwords with argon2id and limits how many hashes run at once, because each
// one allocates the configured memory.
type Hasher struct {
	memoryKiB uint32
	time      uint32
	threads   uint8
	sem       chan struct{}
}

// NewHasher creates a Hasher. maxConcurrent <= 0 means 4.
func NewHasher(memoryKiB, time uint32, threads uint8, maxConcurrent int) *Hasher {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &Hasher{memoryKiB: memoryKiB, time: time, threads: threads, sem: make(chan struct{}, maxConcurrent)}
}

// Hash returns a PHC string: $argon2id$v=19$m=..,t=..,p=..$salt$hash.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h.sem <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, h.time, h.memoryKiB, h.threads, argonKeyLen)
	<-h.sem
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.memoryKiB, h.time, h.threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify checks password against a PHC string produced by Hash. The parameters stored in the
// string are used, so changing the configuration never locks users out.
func (h *Hasher) Verify(password, phc string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	h.sem <- struct{}{}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	<-h.sem
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DummyVerify burns the same time as a real verification, so unknown usernames are not
// distinguishable by response time.
func (h *Hasher) DummyVerify(password string) {
	salt := make([]byte, argonSaltLen)
	h.sem <- struct{}{}
	_ = argon2.IDKey([]byte(password), salt, h.time, h.memoryKiB, h.threads, argonKeyLen)
	<-h.sem
}
