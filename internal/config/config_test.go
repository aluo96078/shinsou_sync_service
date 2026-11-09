package config

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Registration != RegistrationInvite || c.RecordQuota != 200000 || c.Listen != "127.0.0.1:8080" {
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
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Registration != "open" || c.RecordQuota != 5 || len(c.TrustedProxies) != 2 || c.PublicURL != "https://sync.example.com" {
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
	} {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("expected error for %v", m)
		}
	}
}
