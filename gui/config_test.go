package main

import "testing"

func TestDefaultProfileUsesSecureSSLDefault(t *testing.T) {
	p := defaultProfile("prod")
	if p.SkipSSL {
		t.Fatalf("default profile must verify TLS by default")
	}
	if p.UnifiedMount {
		t.Fatalf("default profile should use per-bucket mount mode unless unified is explicitly enabled")
	}
	if p.EntryLimit <= 0 || p.MaxFlushers <= 0 || p.ReadAheadKB <= 0 {
		t.Fatalf("default profile must include non-zero advanced tuning defaults")
	}
	if p.StatCacheTTLSeconds <= 0 || p.HTTPTimeoutSeconds <= 0 || p.RetryIntervalSec <= 0 {
		t.Fatalf("default profile must include non-zero timeout defaults")
	}
}

func TestSanitizedForDiskRemovesCredentials(t *testing.T) {
	c := &AppConfig{
		Profiles: []ConnectionProfile{
			{
				Name:      "prod",
				Endpoint:  "https://example.com",
				AccessKey: "AKIA_TEST",
				SecretKey: "secret-test",
				SkipSSL:   false,
			},
		},
		ActiveProfile: "prod",
		AccessKey:     "legacy-access",
		SecretKey:     "legacy-secret",
	}

	sanitized := c.sanitizedForDisk()
	if sanitized.AccessKey != "" || sanitized.SecretKey != "" {
		t.Fatalf("legacy credentials should not be persisted to disk")
	}
	if len(sanitized.Profiles) != 1 {
		t.Fatalf("expected one profile, got %d", len(sanitized.Profiles))
	}
	if sanitized.Profiles[0].AccessKey != "" || sanitized.Profiles[0].SecretKey != "" {
		t.Fatalf("profile credentials should not be persisted to disk")
	}
	if sanitized.Profiles[0].SkipSSL != c.Profiles[0].SkipSSL {
		t.Fatalf("non-secret profile fields must be preserved")
	}
}
