package sandbox

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// ResolveWorkspace determines the host directory to mount at /workspace and
// enforces the non-overridable safety refusals: never mount the filesystem root,
// the host home, or an ancestor of the host home. flagPath defaults to cwd when
// empty. The returned path is absolute with symlinks evaluated.
func ResolveWorkspace(flagPath string) (string, error) {
	p := flagPath
	if p == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("determining working directory: %w", err)
		}
		p = wd
	}
	p = config.ExpandTilde(p)

	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", p, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("project path does not exist: %q", abs)
	}

	fi, err := os.Stat(real)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("project path is not a directory: %q", real)
	}

	if err := RefuseUnsafeHostPath(real); err != nil {
		return "", err
	}

	return real, nil
}

// RefuseUnsafeHostPath enforces the non-overridable safety refusals for a host
// path that is about to be bind-mounted: never the filesystem root, never the
// host home, never an ancestor of it. path must already be absolute and
// symlink-resolved.
//
// It is exported because the workspace is not the only path that reaches this
// question. The parent .git of a worktree is mounted at its own host location,
// and *which* location comes from a `.git` pointer file inside the workspace —
// a file the agent can rewrite. Without this check, `gitdir: /Users/you/x/y`
// produced `--mount source=/Users/you,target=/Users/you` read-write, and
// `gitdir: /Users/you` produced `source=/,target=/`.
func RefuseUnsafeHostPath(path string) error {
	if isFilesystemRoot(path) {
		return fmt.Errorf("refusing to mount filesystem root %q", path)
	}
	home := hostHome()
	if home == "" {
		return nil
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		realHome = home
	}
	switch {
	case samePath(path, realHome):
		return fmt.Errorf("refusing to mount your home directory %q; cd into a specific project first", path)
	case isAncestorOnDisk(path, realHome):
		return fmt.Errorf("%q is an ancestor of your home directory; too broad to mount safely", path)
	}
	return nil
}

// samePath reports whether two paths name the same directory on disk.
//
// It compares identity (device + inode) rather than strings, because a string
// compare is not a path compare on the filesystems people actually use. macOS
// APFS and Windows NTFS are case-insensitive while EvalSymlinks preserves
// whatever casing the caller typed, so `--project /Users/AmitGhadge` slipped
// past a `real == realHome` test and mounted the home directory; unicode
// normalisation (NFC vs NFD in a home directory name) is the same bug wearing a
// different hat. Identity has neither failure mode.
func samePath(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// isAncestorOnDisk reports whether dir is a strict ancestor of child, walking
// child's parents and comparing by identity. Same reasoning as samePath: this is
// what catches `--project /USERS`, which is a real directory, is not the
// filesystem root, and is `/Users` by another name.
func isAncestorOnDisk(dir, child string) bool {
	cur := filepath.Clean(child)
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return false // reached the filesystem root without a match
		}
		if samePath(dir, parent) {
			return true
		}
		cur = parent
	}
}

// WorkspaceMount builds the /workspace bind mount for the given host path.
func WorkspaceMount(hostPath, target string) runtime.Mount {
	if target == "" {
		target = "/workspace"
	}
	return runtime.Mount{Source: hostPath, Target: target, RO: false}
}

// protectedTargets are container paths that a caller-supplied mount may never
// land on or shadow. Mounting over any of them replaces trusted, image-provided
// files with attacker-supplied ones.
//
// The case that made this necessary: `workdir: /usr/local/bin` in a project
// config moved the *workspace mount target*, dropping the repository on top of
// the directory holding sandbox-firewall. In allowlist mode the container's
// entrypoint is /usr/local/bin/sandbox-firewall and runs as root — so the repo
// supplied the program that root executes, and no egress firewall was ever
// programmed. That is a fail-open in the one code path whose stated contract is
// to fail closed.
//
// sandbox-cli's own mounts (the persisted agent HOME, the cache volumes) are not
// checked against this list: they target HOME by design, and they come from the
// tool rather than from anything a repository can influence.
var protectedTargets = []string{
	"/", "/bin", "/sbin", "/lib", "/lib64",
	"/usr", "/usr/bin", "/usr/sbin", "/usr/lib", "/usr/local", "/usr/local/bin", "/usr/local/sbin",
	"/etc", "/proc", "/sys", "/dev", "/boot", "/run", "/var/run",
}

// ValidateMountTarget refuses a caller-supplied container path that would shadow
// a protected one — either by being it, or by being an ancestor of it (mounting
// /usr hides /usr/local/bin just as effectively as mounting it directly).
func ValidateMountTarget(target string) error {
	t := path.Clean(strings.TrimSpace(target))
	if t == "" || t == "." {
		return fmt.Errorf("mount target must not be empty")
	}
	if !path.IsAbs(t) {
		return fmt.Errorf("mount target %q must be an absolute path inside the container", target)
	}
	for _, p := range protectedTargets {
		if t == p {
			return fmt.Errorf("refusing to mount over %q: it holds files the container's own startup depends on", p)
		}
		if isPathAncestor(t, p) {
			return fmt.Errorf("refusing to mount at %q: it would shadow %q, which holds files the container's own startup depends on", t, p)
		}
	}
	return nil
}

// isPathAncestor reports whether ancestor is a strict parent of child, using
// slash semantics — these are container paths, never host paths.
func isPathAncestor(ancestor, child string) bool {
	a := path.Clean(ancestor)
	c := path.Clean(child)
	if a == c {
		return false
	}
	if a == "/" {
		return true
	}
	return strings.HasPrefix(c, a+"/")
}

func isFilesystemRoot(p string) bool {
	return p == string(filepath.Separator) || p == filepath.VolumeName(p)+string(filepath.Separator)
}

func hostHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// isAncestor reports whether ancestor is a strict parent directory of child.
func isAncestor(ancestor, child string) bool {
	a := filepath.Clean(ancestor)
	c := filepath.Clean(child)
	if a == c {
		return false
	}
	rel, err := filepath.Rel(a, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}
