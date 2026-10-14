package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/gateway"
)

// useGatewayAs points the CLI at a real gateway as user, holding scopes.
func useGatewayAs(t *testing.T, user string, scopes ...string) gateway.Store {
	t.Helper()
	st := useGateway(t)
	secret, _, err := st.CreateKey(bg, user, user, scopes)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), user+".key")
	if err := os.WriteFile(keyFile, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cf, err := loadContexts()
	if err != nil {
		t.Fatal(err)
	}
	ec := cf.Contexts["gw"]
	ec.TokenFile = keyFile
	cf.Contexts["gw"] = ec
	if err := cf.save(); err != nil {
		t.Fatal(err)
	}
	return st
}

// A user makes a key, to stdout or to a new file, lists it and revokes it;
// the gateway's refusals come back as errors.
func TestAPIKeyCommands(t *testing.T) {
	st := useGatewayAs(t, "dana", gateway.ScopeRead, gateway.ScopeCreate)

	out, err := runCLI(t, "api-key", "create", "ci", "--scope", "sandbox:read", "--expires", "7d")
	if err != nil || !regexp.MustCompile(`(?m)^secret:  sgk_\S+$`).MatchString(out) || !strings.Contains(out, "name:    ci") {
		t.Fatalf("create: %q, %v", out, err)
	}
	secret := regexp.MustCompile(`sgk_\S+`).FindString(out)
	if k, ok, _ := st.KeyBySecret(bg, secret); !ok || !k.Personal || k.User != "dana" {
		t.Fatalf("the key in the store: %+v %v", k, ok)
	}

	file := filepath.Join(t.TempDir(), "laptop.key")
	out, err = runCLI(t, "api-key", "create", "laptop", "--scope", "sandbox:read", "--out", file)
	if err != nil || strings.Contains(out, "sgk_") || !strings.Contains(out, "secret:  in "+file) {
		t.Fatalf("create --out: %q, %v", out, err)
	}
	fi, err := os.Stat(file)
	data, _ := os.ReadFile(file)
	if err != nil || fi.Mode().Perm() != 0o600 || !strings.HasPrefix(string(data), "sgk_") {
		t.Fatalf("the key file: %v %v %q", fi, err, data)
	}
	// An existing file is never replaced, and nothing is made.
	if _, err := runCLI(t, "api-key", "create", "again", "--scope", "sandbox:read", "--out", file); err == nil {
		t.Error("--out replaced an existing file")
	}

	for name, args := range map[string][]string{
		"no scope":          {"api-key", "create", "x"},
		"never expires":     {"api-key", "create", "x", "--scope", "sandbox:read", "--expires", "never"},
		"a scope not held":  {"api-key", "create", "x", "--scope", "sandbox:ssh"},
		"the admin scope":   {"api-key", "create", "x", "--scope", "admin"},
		"longer than 366d":  {"api-key", "create", "x", "--scope", "sandbox:read", "--expires", "400d"},
		"someone else's id": {"api-key", "revoke", "key_0000000000000000"},
	} {
		if out, err := runCLI(t, args...); err == nil {
			t.Errorf("%s: no error: %q", name, out)
		}
	}

	out, err = runCLI(t, "api-key", "list")
	if err != nil || !regexp.MustCompile(`\bci\s+sandbox:read\s+.*active`).MatchString(out) || !strings.Contains(out, "laptop") || strings.Contains(out, "again") {
		t.Fatalf("list: %q, %v", out, err)
	}
	id := regexp.MustCompile(`key_\w+`).FindString(regexp.MustCompile(`(?m)^.*\bci\b.*$`).FindString(out))
	if out, err := runCLI(t, "api-key", "revoke", id); err != nil || !strings.Contains(out, "revoked "+id) {
		t.Fatalf("revoke: %q, %v", out, err)
	}
	if _, ok, _ := st.KeyBySecret(bg, secret); ok {
		t.Error("a revoked key still works")
	}
	if out, _ := runCLI(t, "api-key", "list"); !regexp.MustCompile(`\bci\b.*revoked`).MatchString(out) {
		t.Errorf("list after revoke: %q", out)
	}
}
