package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/version"
)

func TestVersionPrintsTheBuildVersion(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "sandbox-cli " + version.Version; strings.TrimSpace(out.String()) != want {
		t.Fatalf("version printed %q, want %q", out.String(), want)
	}
}
