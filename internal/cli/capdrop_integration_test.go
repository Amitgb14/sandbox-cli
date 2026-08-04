//go:build docker_integration

// Enable with: go test -tags docker_integration ./internal/cli
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The allowlist grants the container CAP_KILL, and the whole safety argument for
// that is one sentence: the agent never has it, because changing uid clears the
// capability sets. That is kernel behaviour rather than anything this repository
// controls — setpriv is invoked with neither --inh-caps nor --ambient-caps — so
// it is asserted here rather than reasoned about.
//
// CAP_KILL exists because `--init` makes tini PID 1 as root while the agent runs
// as another uid, and kill(2) needs the capability to cross that boundary; the
// first forwarded SIGWINCH otherwise aborts the container. Granting it to the
// *agent* would hand a compromised process the ability to signal anything in the
// namespace, including the proxy the egress allowlist depends on.
func TestAllowlistAgentHasNoCapabilities(t *testing.T) {
	proj := t.TempDir()
	cfg := config.Default()
	cfg.Network.Mode = "allowlist" // the mode that adds capabilities back

	sess := newTestSession(t, cfg)
	code, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		// Read as the agent, after the privilege drop. All three sets, because
		// an ambient capability survives a uid change and would be the way this
		// goes wrong quietly.
		Command: []string{"sh", "-c",
			`grep -E '^Cap(Prm|Eff|Amb):' /proc/self/status > /workspace/caps.txt; id -u >> /workspace/caps.txt`},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	raw, err := os.ReadFile(filepath.Join(proj, "caps.txt"))
	if err != nil {
		t.Fatalf("reading caps.txt: %v", err)
	}
	out := string(raw)

	// Guard the guard: if the command ran as root the assertion below would pass
	// for the wrong reason, since this is about what survives the *drop*.
	if strings.TrimSpace(lastLine(out)) == "0" {
		t.Fatalf("the command ran as root, so this proves nothing:\n%s", out)
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "Cap") {
			continue
		}
		field := strings.Fields(line)
		if len(field) != 2 {
			continue
		}
		if strings.Trim(field[1], "0") != "" {
			t.Errorf("the agent retained capabilities after the drop: %q\n"+
				"CAP_KILL is granted to the root phase only; a non-zero set here means it leaked",
				line)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
