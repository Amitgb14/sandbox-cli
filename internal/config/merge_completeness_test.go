package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mergedFieldYAML is one user-config document per Config field, each setting that
// field to a recognisably non-zero value.
//
// This table exists because the layered merge is hand-written — `mergeInto` copies
// field by field — so a field added to the struct and not to that function parses
// correctly, validates correctly, and is then **silently dropped**. That is not a
// hypothetical: `sandbox:` shipped that way. It was documented, announced in the
// CHANGELOG, refused from project config, unit-tested by constructing a `Config`
// directly — and inert through the loader, which meant `sandbox: none` in a user
// config was accepted rather than refused.
//
// The test goes through `LoadProfile` rather than calling `mergeInto`, because the
// bug is that the real path skips the field and a test of the function under test
// would have to name the field to miss it.
var mergedFieldYAML = map[string]string{
	"Image":       "image: example/img:9\n",
	"Workdir":     "workdir: /elsewhere\n",
	"User":        "user: someone\n",
	"Home":        "home: /sandbox/other\n",
	"Hostname":    "hostname: boxy\n",
	"Runtime":     "runtime: runsc\n",
	"Engine":      "engine: podman\n",
	"Sandbox":     "sandbox: podman\n",
	"Mounts":      "mounts:\n  - {host: /tmp, container: /d}\n",
	"Env":         "env:\n  SOME_NAME: v\n",
	"EnvAllow":    "env_allow:\n  - SOME_NAME\n",
	"Network":     "network:\n  mode: none\n",
	"Ports":       "ports:\n  - 3000\n",
	"Security":    "security:\n  pids_limit: 99\n",
	"Cache":       "cache:\n  enabled: true\n",
	"Snapshot":    "snapshot:\n  interval: 30s\n",
	"Secrets":     "secrets:\n  TOK:\n    env: SOME_NAME\n",
	"Routing":     "routing: [claude, codex]\n",
	"Providers":   "providers:\n  claude: api.example.com\n",
	"Profile":     "profile: prod\n",
	"PersistAuth": "persist_auth: false\n",
	"Sync":        "sync: false\n",
}

func TestEveryConfigFieldSurvivesTheMerge(t *testing.T) {
	typ := reflect.TypeOf(Config{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		y, ok := mergedFieldYAML[name]
		if !ok {
			t.Errorf("Config.%s has no sample in mergedFieldYAML.\n"+
				"  Add one that sets it to a non-zero value, so a field missing from mergeInto\n"+
				"  fails here rather than being silently dropped out of every config file.", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			cfg := loadUserConfig(t, y)
			got := reflect.ValueOf(cfg).FieldByName(name)
			if got.IsZero() {
				t.Errorf("%s was set in the user config and arrived zero: mergeInto does not copy it,\n"+
					"  so the key parses, validates and does nothing.\n  config was:\n%s", name, y)
			}
		})
	}
	for name := range mergedFieldYAML {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("mergedFieldYAML names %q, which is no longer a Config field", name)
		}
	}
}

// And the consequence that made the dropped field a security question rather than
// a cosmetic one: `Validate` runs on the **merged** config, so a refusal written
// for a value that never survives the merge is a refusal that never fires.
//
// Asserted on `LoadProfile` *then* `Validate`, which is the pair every run makes
// (`cli/root.go` loads, applies flags, then validates). Load alone does not
// validate, and testing load alone is how this test first passed for the wrong
// reason.
func TestAnUnimplementedSandboxKindIsRefusedFromAUserConfig(t *testing.T) {
	for _, kind := range []string{"none", "bwrap"} {
		cfg := loadUserConfig(t, "sandbox: "+kind+"\n")
		if cfg.Sandbox != SandboxKind(kind) {
			t.Errorf("`sandbox: %s` did not survive the merge (got %q), so the refusal below can never fire", kind, cfg.Sandbox)
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("`sandbox: %s` from a user config validated; it isolates nothing", kind)
		} else if !contains(err.Error(), "not implemented") {
			t.Errorf("`sandbox: %s`: %v\n  want the unimplemented refusal", kind, err)
		}
	}
	// A typo survives the merge too, and gets the other sentence.
	cfg := loadUserConfig(t, "sandbox: dokcer\n")
	if err := cfg.Validate(); err == nil || !contains(err.Error(), "want docker, podman, bwrap or none") {
		t.Errorf("a misspelled kind from a user config: %v", err)
	}
}

func loadUserConfig(t *testing.T, yaml string) Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir := filepath.Join(home, ".config", "sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadProfile(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("loading %q: %v", yaml, err)
	}
	return cfg
}
