package creds

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// jwt builds a token whose payload is the given JSON. The header and signature
// are never read, so they are filler — which is itself worth pinning: a change
// that started verifying either would break every caller passing a real token
// from an issuer we have no key for.
func jwt(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestClassify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	tests := []struct {
		name  string
		value string
		want  Lifetime
	}{
		{"github classic pat", "ghp_" + strings.Repeat("a", 36), LongLived},
		{"github fine-grained pat", "github_pat_" + strings.Repeat("b", 20), LongLived},
		{"gitlab pat", "glpat-" + strings.Repeat("c", 20), LongLived},
		{"anthropic key", "sk-ant-api03-" + strings.Repeat("d", 20), LongLived},
		{"openai key", "sk-" + strings.Repeat("e", 40), LongLived},
		{"aws long-lived key id", "AKIAIOSFODNN7EXAMPLE", LongLived},

		{"github app installation token", "ghs_" + strings.Repeat("f", 36), ShortLived},
		{"aws sts key id", "ASIAIOSFODNN7EXAMPLE", ShortLived},

		{"jwt expiring in ten minutes", jwt(`{"exp":1700000600}`), ShortLived},
		{"jwt expiring in a year", jwt(`{"exp":1731536000}`), LongLived},
		{"jwt with no exp", jwt(`{"sub":"someone"}`), LongLived},
		{"jwt already expired", jwt(`{"exp":1699999000}`), ShortLived},

		{"opaque value", "b7c1f0e2d3a4", Unknown},
		{"empty", "", Unknown},
		{"not a jwt despite dots", "a.b.c", Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.value, now)
			if got.Lifetime != tt.want {
				t.Errorf("Classify(%s) = %v (%q), want %v", tt.name, got.Lifetime, got.Detail, tt.want)
			}
		})
	}
}

// The detail is printed to a stream somebody may be logging, so it must describe
// the credential without reproducing any of it.
func TestClassifyDetailNeverEchoesTheValue(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	secret := "ghp_" + strings.Repeat("z", 36)

	got := Classify(secret, now)
	if got.Detail == "" {
		t.Fatal("a recognized credential reported no detail")
	}
	// Any run of the value long enough to be a fragment of the secret.
	for i := 0; i+8 <= len(secret); i++ {
		if strings.Contains(got.Detail, secret[i:i+8]) {
			t.Fatalf("detail %q contains a fragment of the value", got.Detail)
		}
	}
}

// Silence is the answer for most credentials, and it means "nothing was
// recognized" rather than "this one is fine". Pinned because a later change that
// started guessing — treating any opaque string as short-lived, say — would turn
// the absence of a warning into a claim the tool cannot keep.
func TestUnknownIsNotShortLived(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, v := range []string{"opaque", "hunter2", strings.Repeat("x", 64)} {
		if got := Classify(v, now); got.Lifetime != Unknown {
			t.Errorf("Classify(%q) = %v, want Unknown", v, got.Lifetime)
		}
	}
}

// A prefix that extends another must be reported as itself.
func TestMoreSpecificPrefixWins(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	pat := Classify("github_pat_"+strings.Repeat("a", 20), now)
	if !strings.Contains(pat.Detail, "fine-grained") {
		t.Errorf("github_pat_ reported as %q, want the fine-grained token", pat.Detail)
	}
	anthropic := Classify("sk-ant-api03-"+strings.Repeat("a", 20), now)
	if !strings.Contains(anthropic.Detail, "Anthropic") {
		t.Errorf("sk-ant- reported as %q, want the Anthropic key", anthropic.Detail)
	}
}
