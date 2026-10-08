//go:build linux

package firecracker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

func TestParseStatStart(t *testing.T) {
	// A command name with spaces and parentheses: fields are counted from the
	// last ')'.
	stat := "4242 (fire (cr) acker) S 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 3 0 987654 1000 200 18446744073709551615"
	got, err := parseStatStart([]byte(stat))
	if err != nil || got != 987654 {
		t.Fatalf("start %d, %v", got, err)
	}
	if _, err := parseStatStart([]byte("4242 (x) S 1")); err == nil {
		t.Fatal("a short stat line was accepted")
	}
	self, err := procStart(os.Getpid())
	if err != nil || self == 0 || !sameProcess(os.Getpid(), self) || sameProcess(os.Getpid(), self+1) {
		t.Fatalf("this process: start %d, %v", self, err)
	}
}

// One transaction: declare, delete and re-add the table, then the kept guests.
func TestInstallScript(t *testing.T) {
	got := installScript("sandboxd", 3128, 7353, []string{"172.16.0.2", "172.16.0.6"})
	if !strings.HasPrefix(got, "table inet sandboxd\ndelete table inet sandboxd\n"+Ruleset("sandboxd", 3128, 7353)) {
		t.Fatalf("the script does not replace the table first:\n%s", got)
	}
	if !strings.HasSuffix(got, "add element inet sandboxd egress_on { 172.16.0.2, 172.16.0.6 }\n") {
		t.Fatalf("kept guests not added in the same script:\n%s", got)
	}
	if strings.Contains(installScript("sandboxd", 3128, 7353, nil), "add element") {
		t.Fatal("an element line with no guests")
	}
}

// keep.json is host-side and readable by root alone, but environment values
// are never in it, as they are never in vm.json.
func TestKeepFileCarriesNoEnvironmentValue(t *testing.T) {
	dir := t.TempDir()
	b := &Backend{cfg: Config{Keep: true, Logf: func(string, ...any) {}}, vms: map[string]*vm{}}
	v := &vm{id: "sbx_0123456789abcdef", dir: dir, spec: backend.Spec{ID: "sbx_0123456789abcdef", Env: map[string]string{"API_KEY": "s3cret-value"}}}
	b.saveKeep(v)
	data, err := os.ReadFile(filepath.Join(dir, keepFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret-value") {
		t.Fatalf("keep.json holds an environment value: %s", data)
	}
	fi, _ := os.Stat(filepath.Join(dir, keepFile))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("keep.json mode %v", fi.Mode().Perm())
	}
}

// What is not provably the VM an earlier sandboxd left is not taken back.
func TestCheckKeptRefuses(t *testing.T) {
	self, _ := procStart(os.Getpid())
	const id = "sbx_0123456789abcdef"
	good := keepState{Spec: backend.Spec{ID: id}, Tap: -1, UID: -1, PID: os.Getpid(), Start: self}
	cases := []struct {
		name  string
		write func(dir string)
		want  string
	}{
		{"no record", func(string) {}, "no record"},
		{"garbage", func(d string) { os.WriteFile(filepath.Join(d, keepFile), []byte("{"), 0o600) }, "unreadable"},
		{"another sandbox's record", func(d string) {
			ks := good
			ks.Spec.ID = "sbx_fedcba9876543210"
			writeKeep(d, ks)
		}, "unreadable"},
		{"a recycled pid", func(d string) {
			ks := good
			ks.Start = self + 1
			writeKeep(d, ks)
		}, "no longer running"},
		{"networking turned on since", func(d string) {
			ks := good
			ks.Tap = 3
			writeKeep(d, ks)
		}, "networking"},
		{"suspended without its snapshot", func(d string) {
			ks := good
			ks.Suspended = true
			writeKeep(d, ks)
		}, "snapshot is missing"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		c.write(dir)
		b := &Backend{cfg: Config{Keep: true, Logf: func(string, ...any) {}}}
		_, why := b.checkKept(id, dir)
		if !strings.Contains(why, c.want) {
			t.Errorf("%s: %q, want %q", c.name, why, c.want)
		}
	}
}

func writeKeep(dir string, ks keepState) {
	data, _ := json.Marshal(ks)
	_ = os.WriteFile(filepath.Join(dir, keepFile), data, 0o600)
}
