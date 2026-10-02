package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/githard"
)

// The workspace leaves the host as a git bundle and comes back as one. The host
// repository is never mounted into a guest, so nothing the agent writes is a
// file the host's git later reads or runs. Every git command here runs on the
// host in a repository whose contents came back from a guest, so it goes
// through githard: hooks, filters, merge drivers and signing programs named in
// that repository's config do not run.

// SandboxBranch is the branch the agent works on inside the sandbox.
const SandboxBranch = "sandbox"

// Git runs a hardened git command in dir.
func Git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append(githard.Args(dir), args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append(githard.Env(dir), "GIT_TERMINAL_PROMPT=0")...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// RepoRoot is the top of the git repository containing dir, or "" outside one.
func RepoRoot(dir string) string {
	out, err := Git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return out
}

// Session is what the CLI remembers about a sandbox it cloned a repository
// into, so work can be brought back after the run — or later, from another
// command. It lives outside every repository.
type Session struct {
	Sandbox string    `json:"sandbox"`
	Context string    `json:"context"`
	Repo    string    `json:"repo"`
	Base    string    `json:"base"`
	Branch  string    `json:"branch"`
	Started time.Time `json:"started,omitempty"`

	// Checkpoint is the last checkpoint fetched, and when.
	Checkpoint   string    `json:"checkpoint,omitempty"`
	CheckpointAt time.Time `json:"checkpoint_at,omitempty"`
	// Done is set once the work has been brought back (or there was none):
	// the record has nothing left to recover. BroughtBack is the ref, if any.
	Done        bool   `json:"done,omitempty"`
	BroughtBack string `json:"brought_back,omitempty"`
}

// Sessions lists every recorded session, newest first. A record that cannot
// be read is skipped: it was written by this CLI, and one bad file should not
// hide the rest.
func Sessions() ([]Session, error) {
	entries, err := os.ReadDir(filepath.Join(ConfigDir(), "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if s, err := LoadSession(id); err == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// Forget removes a session's record. Refs it fetched are left alone: they are
// in the user's repository, and deleting one is the user's decision.
func Forget(id string) error { return os.Remove(sessionPath(id)) }

// MarkBroughtBack records that a session's work is home. ref is "" when there
// was nothing new.
func (s *Session) MarkBroughtBack(ref string) {
	s.Done, s.BroughtBack = true, ref
	_ = s.Save()
}

func sessionPath(id string) string { return filepath.Join(ConfigDir(), "sessions", id+".json") }

func (s Session) Save() error {
	if err := os.MkdirAll(filepath.Dir(sessionPath(s.Sandbox)), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(sessionPath(s.Sandbox), b, 0o600)
}

func LoadSession(id string) (Session, error) {
	var s Session
	b, err := os.ReadFile(sessionPath(id))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// CloneIn bundles the repository's HEAD and clones it into the sandbox on
// SandboxBranch. It returns the base commit.
func CloneIn(ctx context.Context, c *api.Client, sandbox, repo string) (string, error) {
	base, err := Git(repo, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("the repository has no commit to start from: %w", err)
	}
	if dirty, _ := Git(repo, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		fmt.Fprintln(os.Stderr, "sandbox-cli: note: uncommitted changes stay on the host; the sandbox starts from HEAD "+base[:12])
	}
	tmp, err := os.CreateTemp("", "sbx-in-*.bundle")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if _, err := Git(repo, "bundle", "create", "--quiet", tmp.Name(), "HEAD"); err != nil {
		return "", err
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := c.PutWorkspace(ctx, sandbox, SandboxBranch, f); err != nil {
		return "", fmt.Errorf("cloning into the sandbox: %w", err)
	}
	return base, nil
}

// BringBack commits whatever the agent left uncommitted, bundles base..branch
// out of the sandbox, verifies it, and fetches it into refs/sandbox/<name>. It
// never touches a branch: what came back is the agent's work, and deciding to
// merge it is the person's.
//
// It returns the ref, or "" when there was nothing new.
func BringBack(ctx context.Context, c *api.Client, s Session, name string) (string, error) {
	// Names that are namespaces of their own: refs/sandbox/fleet/<branch> and
	// refs/sandbox/checkpoints/<id> cannot coexist with a ref of the same name.
	if name == "fleet" || name == "checkpoints" {
		return "", fmt.Errorf("refs/sandbox/%s is reserved; bring it back under another name (--name)", name)
	}
	commit := "cd /workspace && git add -A && " +
		"(git diff --cached --quiet || git -c user.name=sandbox -c user.email=sandbox@localhost commit -q -m 'sandbox: uncommitted work at the end of the run')"
	if res, err := c.Run(ctx, s.Sandbox, api.RunRequest{Argv: []string{"sh", "-c", commit}}); err != nil {
		return "", err
	} else if res.ExitCode != 0 {
		return "", fmt.Errorf("committing in the sandbox: %s", strings.TrimSpace(string(res.Stderr)))
	}

	tmp, err := os.CreateTemp("", "sbx-out-*.bundle")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	err = c.GetWorkspaceBundle(ctx, s.Sandbox, s.Base, s.Branch, tmp)
	tmp.Close()
	if api.IsCode(err, api.CodeConflict) {
		return "", nil // nothing new on the branch
	}
	if err != nil {
		return "", err
	}

	// The bundle is from the guest: check it before fetching from it. It must
	// verify against this repository (its prerequisite is the base we sent) and
	// carry exactly the one ref we asked for.
	if _, err := Git(s.Repo, "bundle", "verify", "--quiet", tmp.Name()); err != nil {
		return "", fmt.Errorf("the bundle from the sandbox does not verify: %w", err)
	}
	heads, err := Git(s.Repo, "bundle", "list-heads", tmp.Name())
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(heads), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], " refs/heads/"+s.Branch) {
		return "", errors.New("the bundle from the sandbox does not carry exactly the expected branch; not fetching it")
	}
	ref := "refs/sandbox/" + name
	if _, err := Git(s.Repo, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--force", tmp.Name(),
		"refs/heads/"+s.Branch+":"+ref); err != nil {
		return "", err
	}
	return ref, nil
}

// ConfigDir is sandbox-cli's own configuration directory on the host.
func ConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "sandbox")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sandbox")
}

// Identity is the git identity a sandbox's commits are made with, as the
// environment variables git reads. Without one, git in the guest refuses every
// commit ("Author identity unknown") — the image sets none, and should not:
// whose name goes on the work is the person's choice, not the image's.
//
// The default is neutral, the same one bring-back's own commit uses. Your own
// user.name and user.email are used only when asked for (--git, or git: true in
// a fleet), because they are personal data crossing into a guest that may be
// running somebody else's instructions. They are read from the repository's
// config through githard, which reading a value does not need but every host
// git call gets.
func Identity(repo string, own bool) map[string]string {
	name, email := "sandbox", "sandbox@localhost"
	if own {
		if n, err := Git(repo, "config", "--get", "user.name"); err == nil && n != "" {
			name = n
		}
		if e, err := Git(repo, "config", "--get", "user.email"); err == nil && e != "" {
			email = e
		}
	}
	return map[string]string{
		"GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
		"GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email,
	}
}
