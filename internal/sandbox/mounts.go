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

	if isFilesystemRoot(real) {
		return "", fmt.Errorf("refusing to mount filesystem root %q as the workspace", real)
	}

	if home := hostHome(); home != "" {
		realHome, herr := filepath.EvalSymlinks(home)
		if herr != nil {
			realHome = home
		}
		switch {
		case real == realHome:
			return "", fmt.Errorf("refusing to mount your home directory %q; cd into a specific project first", real)
		case isAncestor(real, realHome):
			return "", fmt.Errorf("%q is an ancestor of your home directory; too broad to mount safely", real)
		}
	}

	return real, nil
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
