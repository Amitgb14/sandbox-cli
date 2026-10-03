package sandbox

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// TestProdNeverRendersTheSeccompPolicyAsAProfilePath is the regression for a
// blocker that shipped past both a green suite and a manual check.
//
// SeccompRequired is a policy — "refuse unless the daemon applies a filter" —
// acted on by Session.enforceSeccomp. It was also being handed to docker, which
// treats any seccomp= value other than "unconfined" as a path to a profile file.
// Every prod run on a daemon that *did* apply seccomp therefore died with
// "opening seccomp profile (required) failed: open required: no such file".
//
// It went unnoticed because the only machine it was tried on reports
// profile=unconfined, so enforceSeccomp refused first and the container was
// never reached. The verification exercised one of two branches and read as
// though it had covered the feature.
func TestProdNeverRendersTheSeccompPolicyAsAProfilePath(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Profile = config.ProfileProd
	cfg.Security.Seccomp = config.SeccompRequired

	spec, err := BuildSpec(cfg, Options{Project: dir, Allow: []string{"api.example.com"}, Command: []string{"true"}})
	if err != nil {
		t.Fatalf("prod config could not be turned into a spec: %v", err)
	}
	if spec.Seccomp == config.SeccompRequired {
		t.Error("the seccomp policy sentinel reached RunSpec; docker would read it as a file path")
	}
	argv := strings.Join(runtime.BuildArgs(spec), " ")
	if strings.Contains(argv, "seccomp="+config.SeccompRequired) {
		t.Errorf("docker argv contains the policy sentinel as a profile path:\n%s", argv)
	}
	// And the policy is not silently turned into something weaker either.
	if strings.Contains(argv, "seccomp=unconfined") {
		t.Errorf("the seccomp policy was rendered as unconfined:\n%s", argv)
	}
}

// An explicit profile path is still passed through — the sentinel is the only
// value with special meaning.
func TestAnExplicitSeccompProfileStillReachesDocker(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Network.Mode = "default"
	cfg.Security.Seccomp = "/etc/sandbox/seccomp.json"

	spec, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(runtime.BuildArgs(spec), " "), "seccomp=/etc/sandbox/seccomp.json") {
		t.Error("an explicitly configured seccomp profile did not reach docker")
	}
}
