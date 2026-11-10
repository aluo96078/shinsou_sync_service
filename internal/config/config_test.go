package config

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Registration != RegistrationInvite || c.RecordQuota != 200000 || c.Listen != "127.0.0.1:8080" ||
		c.Sharing != SharingEnabled || c.SharesPerDay != 200 || c.SocialRatePerMinute != 120 || c.SocialRateBurst != 40 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"SHINSOU_REGISTRATION":        "open",
		"SHINSOU_RECORD_QUOTA":        "5",
		"SHINSOU_TRUSTED_PROXIES":     "10.0.0.0/8, 127.0.0.1",
		"SHINSOU_PUBLIC_URL":          "https://sync.example.com/",
		"SHINSOU_TOMBSTONE_RETENTION": "1h",
		"SHINSOU_SHARING":             "disabled",
		"SHINSOU_SHARES_PER_DAY":      "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Registration != "open" || c.RecordQuota != 5 || len(c.TrustedProxies) != 2 || c.PublicURL != "https://sync.example.com" ||
		c.Sharing != SharingDisabled || c.SharesPerDay != 0 {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for _, m := range []map[string]string{
		{"SHINSOU_REGISTRATION": "maybe"},
		{"SHINSOU_RECORD_QUOTA": "-1"},
		{"SHINSOU_TRUSTED_PROXIES": "nope"},
		{"SHINSOU_JOB_INTERVAL": "soon"},
		{"SHINSOU_EMAIL_VERIFICATION": "x"},
		{"SHINSOU_SHARING": "on"},
		{"SHINSOU_SHARES_PER_DAY": "-1"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("expected error for %v", m)
		}
	}
}

func smtpEnv(extra map[string]string) map[string]string {
	m := map[string]string{
		"SHINSOU_SMTP_HOST":  "smtp.example.com",
		"SHINSOU_SMTP_FROM":  "Shinsou <no-reply@example.com>",
		"SHINSOU_PUBLIC_URL": "https://sync.example.com",
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestSMTPDefaultsAndAvailability(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.EmailAvailable() || c.SMTPPort != 587 || c.SMTPTLS != SMTPStartTLS || c.EmailVerification != EmailDisabled {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	c, err = Load(env(smtpEnv(map[string]string{"SHINSOU_SMTP_PORT": "465", "SHINSOU_SMTP_TLS": "implicit"})))
	if err != nil {
		t.Fatal(err)
	}
	if !c.EmailAvailable() || c.SMTPPort != 465 || c.SMTPTLS != SMTPImplicit {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestEmailVerificationDefault(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no smtp", map[string]string{"SHINSOU_REGISTRATION": "open"}, EmailDisabled},
		{"smtp, invite registration", smtpEnv(nil), EmailDisabled},
		{"smtp, open registration", smtpEnv(map[string]string{"SHINSOU_REGISTRATION": "open"}), EmailRequired},
		{"explicit wins", smtpEnv(map[string]string{"SHINSOU_REGISTRATION": "open", "SHINSOU_EMAIL_VERIFICATION": "optional"}), EmailOptional},
		{"explicit disabled wins", smtpEnv(map[string]string{"SHINSOU_REGISTRATION": "open", "SHINSOU_EMAIL_VERIFICATION": "disabled"}), EmailDisabled},
	}
	for _, tc := range cases {
		c, err := Load(env(tc.env))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.EmailVerification != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, c.EmailVerification, tc.want)
		}
	}
}

func TestSMTPValidation(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"host without from":         {"SHINSOU_SMTP_HOST": "smtp.example.com", "SHINSOU_PUBLIC_URL": "https://x.example"},
		"smtp without public url":   {"SHINSOU_SMTP_HOST": "smtp.example.com", "SHINSOU_SMTP_FROM": "a@example.com"},
		"bad tls":                   smtpEnv(map[string]string{"SHINSOU_SMTP_TLS": "ssl"}),
		"bad port":                  smtpEnv(map[string]string{"SHINSOU_SMTP_PORT": "70000"}),
		"zero port":                 smtpEnv(map[string]string{"SHINSOU_SMTP_PORT": "0"}),
		"username without password": smtpEnv(map[string]string{"SHINSOU_SMTP_USERNAME": "u"}),
		"bad from":                  smtpEnv(map[string]string{"SHINSOU_SMTP_FROM": "not an address"}),
		"relative public url":       smtpEnv(map[string]string{"SHINSOU_PUBLIC_URL": "sync.example.com"}),
		"required without smtp":     {"SHINSOU_EMAIL_VERIFICATION": "required"},
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Load(env(smtpEnv(map[string]string{"SHINSOU_SMTP_USERNAME": "u", "SHINSOU_SMTP_PASSWORD": "p w", "SHINSOU_EMAIL_VERIFICATION": "required"}))); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
}
