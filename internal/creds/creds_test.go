package creds

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_AllSources(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("  file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDS_TEST_ENV", "env-secret")

	vars, err := Resolve(map[string]Source{
		"FROM_FILE": {File: file},
		"FROM_CMD":  {Command: "printf cmd-secret"},
		"FROM_ENV":  {Env: "CREDS_TEST_ENV"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by name: FROM_CMD, FROM_ENV, FROM_FILE.
	got := map[string]string{}
	for _, v := range vars {
		got[v.Name] = v.Value
	}
	if got["FROM_FILE"] != "file-secret" {
		t.Errorf("file value = %q, want trimmed file-secret", got["FROM_FILE"])
	}
	if got["FROM_CMD"] != "cmd-secret" {
		t.Errorf("cmd value = %q, want cmd-secret", got["FROM_CMD"])
	}
	if got["FROM_ENV"] != "env-secret" {
		t.Errorf("env value = %q, want env-secret", got["FROM_ENV"])
	}
	// Deterministic, sorted order.
	if vars[0].Name != "FROM_CMD" || vars[2].Name != "FROM_FILE" {
		t.Errorf("expected sorted order, got %v", vars)
	}
}

func TestResolve_Errors(t *testing.T) {
	cases := map[string]Source{
		"missing file": {File: "/no/such/secret/file"},
		"failing cmd":  {Command: "exit 3"},
		"unset env":    {Env: "CREDS_DEFINITELY_UNSET_XYZ"},
		"no source":    {},
	}
	for name, src := range cases {
		if _, err := Resolve(map[string]Source{"X": src}); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}
