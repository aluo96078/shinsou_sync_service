// Package config loads the server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
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

// SMTP transport security modes.
const (
	SMTPStartTLS = "starttls"
	SMTPImplicit = "implicit"
	SMTPNone     = "none"
)

// Email verification policies.
const (
	EmailDisabled = "disabled"
	EmailOptional = "optional"
	EmailRequired = "required"
)

// Feature switch values.
const (
	SharingEnabled  = "enabled"
	SharingDisabled = "disabled"
	// Enabled and Disabled are the same values under names that do not mention sharing.
	Enabled  = "enabled"
	Disabled = "disabled"
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

	// SMTP. Mail is sent only when SMTPHost and SMTPFrom are both set (see EmailAvailable).
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLS      string

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
	// Per account: user search and share creation together.
	SocialRatePerMinute int
	SocialRateBurst     int

	// Sharing is SharingEnabled or SharingDisabled; SharesPerDay caps new shares per account (0 = unlimited).
	Sharing      string
	SharesPerDay int

	// Reviews switches reviews on or off; VisitorBrowsing lets people who are not signed in read
	// work pages and reviews. Both are Enabled or Disabled.
	Reviews         string
	VisitorBrowsing string
	// ReviewsPerDay caps the reviews and replies one account writes in 24 hours, ReportsPerDay its
	// reports (0 = unlimited). An account younger than NewAccountAge is held to
	// NewAccountPostsPerDay instead when that is lower.
	ReviewsPerDay         int
	ReportsPerDay         int
	NewAccountAge         time.Duration
	NewAccountPostsPerDay int

	JobInterval        time.Duration
	TombstoneRetention time.Duration
	BatchRetention     time.Duration

	LogLevel string
}

// Defaults returns the configuration used when no environment variable is set.
func Defaults() Config {
	return Config{
		Listen:                "127.0.0.1:8080",
		Registration:          RegistrationInvite,
		EmailVerification:     EmailDisabled,
		SMTPPort:              587,
		SMTPTLS:               SMTPStartTLS,
		InstanceName:          "Shinsou Sync",
		RecordQuota:           200000,
		AccessTokenTTL:        15 * time.Minute,
		RefreshTokenTTL:       90 * 24 * time.Hour,
		Argon2MemoryKiB:       64 * 1024,
		Argon2Time:            3,
		Argon2Threads:         1,
		AuthRatePerMinute:     30,
		AuthRateBurst:         10,
		PushRatePerMinute:     240,
		PushRateBurst:         60,
		SocialRatePerMinute:   120,
		SocialRateBurst:       40,
		Sharing:               SharingEnabled,
		SharesPerDay:          200,
		Reviews:               Enabled,
		VisitorBrowsing:       Disabled,
		ReviewsPerDay:         30,
		ReportsPerDay:         20,
		NewAccountAge:         72 * time.Hour,
		NewAccountPostsPerDay: 5,
		JobInterval:           time.Hour,
		TombstoneRetention:    90 * 24 * time.Hour,
		BatchRetention:        24 * time.Hour,
		LogLevel:              "info",
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

	verificationSet := strings.TrimSpace(getenv("SHINSOU_EMAIL_VERIFICATION")) != ""

	str("SHINSOU_DATABASE_URL", &c.DatabaseURL)
	str("SHINSOU_LISTEN", &c.Listen)
	str("SHINSOU_PUBLIC_URL", &c.PublicURL)
	str("SHINSOU_REGISTRATION", &c.Registration)
	str("SHINSOU_EMAIL_VERIFICATION", &c.EmailVerification)
	str("SHINSOU_SMTP_HOST", &c.SMTPHost)
	str("SHINSOU_SMTP_USERNAME", &c.SMTPUsername)
	if v := getenv("SHINSOU_SMTP_PASSWORD"); v != "" { // passwords may legitimately carry spaces
		c.SMTPPassword = v
	}
	str("SHINSOU_SMTP_FROM", &c.SMTPFrom)
	str("SHINSOU_SMTP_TLS", &c.SMTPTLS)
	num("SHINSOU_SMTP_PORT", &c.SMTPPort)
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
	num("SHINSOU_SOCIAL_RATE_PER_MINUTE", &c.SocialRatePerMinute)
	num("SHINSOU_SOCIAL_RATE_BURST", &c.SocialRateBurst)
	num("SHINSOU_SHARES_PER_DAY", &c.SharesPerDay)
	str("SHINSOU_SHARING", &c.Sharing)
	str("SHINSOU_REVIEWS", &c.Reviews)
	str("SHINSOU_VISITOR_BROWSING", &c.VisitorBrowsing)
	num("SHINSOU_REVIEWS_PER_DAY", &c.ReviewsPerDay)
	num("SHINSOU_REPORTS_PER_DAY", &c.ReportsPerDay)
	num("SHINSOU_NEW_ACCOUNT_POSTS_PER_DAY", &c.NewAccountPostsPerDay)
	dur("SHINSOU_NEW_ACCOUNT_AGE", &c.NewAccountAge)
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
	switch c.Sharing {
	case SharingEnabled, SharingDisabled:
	default:
		errs = append(errs, fmt.Errorf("SHINSOU_SHARING: want enabled or disabled, got %q", c.Sharing))
	}
	for _, sw := range []struct{ key, value string }{{"SHINSOU_REVIEWS", c.Reviews}, {"SHINSOU_VISITOR_BROWSING", c.VisitorBrowsing}} {
		if sw.value != Enabled && sw.value != Disabled {
			errs = append(errs, fmt.Errorf("%s: want enabled or disabled, got %q", sw.key, sw.value))
		}
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	errs = append(errs, c.validateMail(verificationSet)...)
	if c.Argon2MemoryKiB < 8 || c.Argon2Time < 1 {
		errs = append(errs, errors.New("argon2 parameters are too small"))
	}
	return c, errors.Join(errs...)
}

// EmailAvailable reports whether the server can send mail.
func (c Config) EmailAvailable() bool { return c.SMTPHost != "" && c.SMTPFrom != "" }

// validateMail checks the SMTP settings and settles the default verification policy. It runs
// after the other values are parsed because the default depends on the registration policy.
func (c *Config) validateMail(verificationSet bool) []error {
	var errs []error
	switch c.SMTPTLS {
	case SMTPStartTLS, SMTPImplicit, SMTPNone:
	default:
		errs = append(errs, fmt.Errorf("SHINSOU_SMTP_TLS: want starttls, implicit or none, got %q", c.SMTPTLS))
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		errs = append(errs, fmt.Errorf("SHINSOU_SMTP_PORT: want 1-65535, got %d", c.SMTPPort))
	}
	if (c.SMTPUsername == "") != (c.SMTPPassword == "") {
		errs = append(errs, errors.New("SHINSOU_SMTP_USERNAME and SHINSOU_SMTP_PASSWORD must be set together"))
	}
	if c.SMTPHost != "" && c.SMTPFrom == "" {
		errs = append(errs, errors.New("SHINSOU_SMTP_FROM is required when SHINSOU_SMTP_HOST is set"))
	}
	if c.SMTPFrom != "" {
		if strings.ContainsAny(c.SMTPFrom, "\r\n") {
			errs = append(errs, errors.New("SHINSOU_SMTP_FROM must not contain line breaks"))
		} else if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
			errs = append(errs, fmt.Errorf("SHINSOU_SMTP_FROM: not a valid address: %v", err))
		}
	}
	if c.EmailAvailable() {
		u, err := url.Parse(c.PublicURL)
		if c.PublicURL == "" {
			errs = append(errs, errors.New("SHINSOU_PUBLIC_URL is required when SMTP is configured (mail links need it)"))
		} else if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("SHINSOU_PUBLIC_URL: want an absolute http(s) URL, got %q", c.PublicURL))
		}
	}
	if !verificationSet {
		c.EmailVerification = EmailDisabled
		if c.EmailAvailable() && c.Registration == RegistrationOpen {
			c.EmailVerification = EmailRequired
		}
	}
	if c.EmailVerification == EmailRequired && !c.EmailAvailable() {
		errs = append(errs, errors.New("SHINSOU_EMAIL_VERIFICATION=required needs SMTP (set SHINSOU_SMTP_HOST and SHINSOU_SMTP_FROM)"))
	}
	return errs
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
