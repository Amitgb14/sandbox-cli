package fleet

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// landRepo has a main branch and, under refs/sandbox/fleet/, work for three
// tasks: one verified, one rejected by its verify, one with nothing new.
func landRepo(t *testing.T) *State {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	// land commits its merge as the user, from the user's own config. A test
	// machine may have none — CI does not, and git refuses to guess there — so
	// the repository carries one, as a user's machine would.
	gitT(t, repo, "config", "user.name", "t")
	gitT(t, repo, "config", "user.email", "t@x")
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	base := gitT(t, repo, "rev-parse", "HEAD")
	work := func(name string) string {
		gitT(t, repo, "checkout", "-q", "-b", "w-"+name, base)
		os.WriteFile(filepath.Join(repo, name+".txt"), []byte(name), 0o644)
		gitT(t, repo, "add", name+".txt")
		gitT(t, repo, "commit", "-q", "-m", name)
		gitT(t, repo, "update-ref", "refs/sandbox/fleet/"+name, "HEAD")
		gitT(t, repo, "checkout", "-q", "main")
		gitT(t, repo, "branch", "-q", "-D", "w-"+name)
		return "refs/sandbox/fleet/" + name
	}
	return &State{Repo: repo, BaseBranch: "main", BaseCommit: base, Tasks: map[string]*TaskState{
		"good":    {Branch: "good", State: TaskVerified, Ref: work("good")},
		"bad":     {Branch: "bad", State: TaskRejected, ExitCode: VerifyFailedExit, Ref: work("bad")},
		"empty":   {Branch: "empty", State: TaskVerified},
		"running": {Branch: "running", State: TaskRunning},
	}}
}

func TestLandRefusesWhatIsNotReady(t *testing.T) {
	st := landRepo(t)
	for branch, want := range map[string]error{
		"bad": ErrNotVerified, "empty": ErrNothingToLand, "running": ErrAgentRunning, "nope": ErrUnknownBranch,
	} {
		if err := Land(st, branch, LandOptions{}); !errors.Is(err, want) {
			t.Errorf("%s: err = %v; want %v", branch, err, want)
		}
	}
	if err := Land(st, "good", LandOptions{}); err != nil {
		t.Fatal(err)
	}
	if msg := gitT(t, st.Repo, "log", "-1", "--format=%s"); !strings.HasPrefix(msg, "Land good") {
		t.Errorf("merge commit %q", msg)
	}
	if err := Land(st, "bad", LandOptions{Unverified: true}); err != nil {
		t.Fatalf("--unverified: %v", err)
	}
}

// Refusals about the base stop land --all; refusals about a branch skip it.
func TestLandAllSkipsBranchesAndStopsOnTheBase(t *testing.T) {
	st := landRepo(t)
	landed, skipped, err := LandAll(st, LandOptions{})
	if err != nil || len(landed) != 1 || landed[0] != "good" || len(skipped) != 3 {
		t.Fatalf("landed %v skipped %v err %v", landed, skipped, err)
	}

	st = landRepo(t)
	os.WriteFile(filepath.Join(st.Repo, "dirty.txt"), []byte("x"), 0o644)
	gitT(t, st.Repo, "add", "dirty.txt")
	if _, _, err := LandAll(st, LandOptions{}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("a dirty base: %v", err)
	}

	st = landRepo(t)
	gitT(t, st.Repo, "checkout", "-q", "-b", "elsewhere")
	if err := Land(st, "good", LandOptions{}); err == nil || !strings.Contains(err.Error(), "--onto") {
		t.Fatalf("a moved checkout: %v", err)
	}
	if err := Land(st, "good", LandOptions{Onto: "elsewhere"}); err != nil {
		t.Fatalf("--onto: %v", err)
	}
}

func TestParseLimits(t *testing.T) {
	for in, want := range map[string]int{"4g": 4096, "512m": 512, "2048": 2048, "": 0, "lots": 0} {
		if got := parseMemoryMiB(in); got != want {
			t.Errorf("memory %q = %d, want %d", in, got, want)
		}
	}
	if sandboxName("feature/Login_Flow") != "fleet-feature-login-flow" {
		t.Errorf("name %q", sandboxName("feature/Login_Flow"))
	}
}
