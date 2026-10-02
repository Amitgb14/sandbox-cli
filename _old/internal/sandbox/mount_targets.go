package sandbox

// Guest-side mount target checks, split from mounts.go when its host-path half
// moved to internal/hostpath (rewrite M1). Ported with the macOS backend (M6),
// the only one with bind mounts.

import (
	"fmt"
	"path"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

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
// ValidateMountPath rejects a host path or container target that docker's
// `--mount` CSV syntax cannot express unambiguously.
//
// The renderer builds `type=bind,source=<src>,target=<tgt>`, so a comma in
// either value is read as the start of another option: a directory named "a,b"
// produced `source=/tmp/a,b,target=/data`, where docker sees a field `b`.
// Nothing good is on the other side of that, and quoting is not reliably
// supported, so it is refused where the path enters rather than mangled here.
func ValidateMountPath(kind, p string) error {
	if strings.ContainsRune(p, ',') {
		return fmt.Errorf("mount %s %q contains a comma, which docker's --mount syntax cannot express; "+
			"rename the directory or mount a parent of it", kind, p)
	}
	return nil
}

func ValidateMountTarget(target string) error {
	if err := ValidateMountPath("target", target); err != nil {
		return err
	}
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
