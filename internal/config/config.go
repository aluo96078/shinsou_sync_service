// Package config loads the server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Registration policies.
const (
	RegistrationOpen   = "open"
	RegistrationInvite = "invite"
	RegistrationClosed = "closed"
)

// Email verification policies. Verification itself arrives with SMTP support in a later phase.
const (
	EmailDisabled = "disabled"
	EmailOptional = "optional"
	EmailRequired = "required"
)

// Config is the complete runtime configuration.
type Config struct {
	DatabaseURL string
	Listen      string
	PublicURL   string

	Registration      string
	EmailVerification string

	InstanceName        string
	InstanceDescription string
	TermsURL            string
	PrivacyURL          string
	Contact             string

	TrustedProxies []netip.Prefix

	RecordQuota int

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	Argon2MemoryKiB uint32
	Argon2Time      uint32
	Argon2Threads   uint8

	// Rate limits: requests per minute with a burst allowance.
	AuthRatePerMinute int
	AuthRateBurst     int
	PushRatePerMinute int
	PushRateBurst     int

	JobInterval        time.Duration
	TombstoneRetention time.Duration
	BatchRetention     time.Duration

	LogLevel string
}

// Defaults returns the configuration used when no environment variable is set.
func Defaults() Config {
	return Config{
		Listen:             "127.0.0.1:8080",
		Registration:       RegistrationInvite,
		EmailVerification:  EmailDisabled,
		InstanceName:       "Shinsou Sync",
		RecordQuota:        200000,
		AccessTokenTTL:     15 * time.Minute,
		RefreshTokenTTL:    90 * 24 * time.Hour,
		Argon2MemoryKiB:    64 * 1024,
		Argon2Time:         3,
		Argon2Threads:      1,
		AuthRatePerMinute:  30,
		AuthRateBurst:      10,
		PushRatePerMinute:  240,
		PushRateBurst:      60,
		JobInterval:        time.Hour,
		TombstoneRetention: 90 * 24 * time.Hour,
		BatchRetention:     24 * time.Hour,
		LogLevel:           "info",
	}
}

// Load reads the configuration through getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	c := Defaults()
	var errs []error
	str := func(key string, dst *string) {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			*dst = v
		}
	}
	num := func(key string, dst *int) {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				errs = append(errs, fmt.Errorf("%s: want a non-negative integer, got %q", key, v))
				return
			}
			*dst = n
		}
	}
	dur := func(key string, dst *time.Duration) {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 {
				errs = append(errs, fmt.Errorf("%s: want a duration such as 90m or 720h, got %q", key, v))
				return
			}
			*dst = d
		}
	}

	str("SHINSOU_DATABASE_URL", &c.DatabaseURL)
	str("SHINSOU_LISTEN", &c.Listen)
	str("SHINSOU_PUBLIC_URL", &c.PublicURL)
	str("SHINSOU_REGISTRATION", &c.Registration)
	str("SHINSOU_EMAIL_VERIFICATION", &c.EmailVerification)
	str("SHINSOU_INSTANCE_NAME", &c.InstanceName)
	str("SHINSOU_INSTANCE_DESCRIPTION", &c.InstanceDescription)
	str("SHINSOU_TERMS_URL", &c.TermsURL)
	str("SHINSOU_PRIVACY_URL", &c.PrivacyURL)
	str("SHINSOU_CONTACT", &c.Contact)
	str("SHINSOU_LOG_LEVEL", &c.LogLevel)
	num("SHINSOU_RECORD_QUOTA", &c.RecordQuota)
	num("SHINSOU_AUTH_RATE_PER_MINUTE", &c.AuthRatePerMinute)
	num("SHINSOU_AUTH_RATE_BURST", &c.AuthRateBurst)
	num("SHINSOU_PUSH_RATE_PER_MINUTE", &c.PushRatePerMinute)
	num("SHINSOU_PUSH_RATE_BURST", &c.PushRateBurst)
	dur("SHINSOU_ACCESS_TOKEN_TTL", &c.AccessTokenTTL)
	dur("SHINSOU_REFRESH_TOKEN_TTL", &c.RefreshTokenTTL)
	dur("SHINSOU_JOB_INTERVAL", &c.JobInterval)
	dur("SHINSOU_TOMBSTONE_RETENTION", &c.TombstoneRetention)
	dur("SHINSOU_BATCH_RETENTION", &c.BatchRetention)

	mem := int(c.Argon2MemoryKiB)
	tm := int(c.Argon2Time)
	num("SHINSOU_ARGON2_MEMORY_KIB", &mem)
	num("SHINSOU_ARGON2_TIME", &tm)
	c.Argon2MemoryKiB, c.Argon2Time = uint32(mem), uint32(tm)

	if v := strings.TrimSpace(getenv("SHINSOU_TRUSTED_PROXIES")); v != "" {
		prefixes, err := ParsePrefixes(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("SHINSOU_TRUSTED_PROXIES: %w", err))
		}
		c.TrustedProxies = prefixes
	}

	switch c.Registration {
	case RegistrationOpen, RegistrationInvite, RegistrationClosed:
	default:
		errs = append(errs, fmt.Errorf("SHINSOU_REGISTRATION: want open, invite or closed, got %q", c.Registration))
	}
	switch c.EmailVerification {
	case EmailDisabled, EmailOptional, EmailRequired:
	default:
		errs = append(errs, fmt.Errorf("SHINSOU_EMAIL_VERIFICATION: want disabled, optional or required, got %q", c.EmailVerification))
	}
	if c.Argon2MemoryKiB < 8 || c.Argon2Time < 1 {
		errs = append(errs, errors.New("argon2 parameters are too small"))
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	return c, errors.Join(errs...)
}

// RequireDatabase fails when no database URL is configured.
func (c Config) RequireDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("SHINSOU_DATABASE_URL is required")
	}
	return nil
}

// ParsePrefixes parses a comma-separated list of IPs and CIDR ranges.
func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "/") {
			p, err := netip.ParsePrefix(part)
			if err != nil {
				return nil, fmt.Errorf("bad CIDR %q", part)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("bad IP %q", part)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}
