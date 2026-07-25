package runtime

import (
	"sort"
	"strconv"
)

// BuildArgs converts a RunSpec into the argument vector for `docker`. It is a
// pure function (no I/O, deterministic) so the isolation invariants can be
// asserted exhaustively in unit tests: only the declared mounts are ever
// host-connected, HOME is always the fake ephemeral path, and the host home is
// never mounted.
func BuildArgs(s RunSpec) []string {
	a := []string{"run", "--init"} // --init reaps zombie children from agent subprocesses

	if s.Runtime != "" {
		a = append(a, "--runtime", s.Runtime)
	}
	if s.Remove {
		a = append(a, "--rm")
	}
	if s.Name != "" {
		a = append(a, "--name", s.Name)
	}
	// Labels, in sorted-key order so the rendered command is deterministic (same
	// treatment as Env below, and for the same reason: --dry-run is a golden test).
	for _, k := range sortedKeys(s.Labels) {
		a = append(a, "--label", k+"="+s.Labels[k])
	}
	// A detached container has nobody attached to it: no pty to allocate and no
	// stdin to keep open. -d replaces -i/-it rather than joining them.
	switch {
	case s.Detach:
		a = append(a, "-d")
	case s.TTY:
		a = append(a, "-it")
	default:
		a = append(a, "-i")
	}
	if s.Hostname != "" {
		a = append(a, "--hostname", s.Hostname)
	}
	if s.User != "" {
		a = append(a, "--user", s.User)
	}
	if s.Network != "" {
		a = append(a, "--network", s.Network)
	}
	for _, h := range s.AddHosts {
		a = append(a, "--add-host", h)
	}
	// Published ports. Emitted verbatim: the address is already explicit (see
	// RunSpec.Ports), so what is exposed is decided in one place, upstream, and
	// is visible in --dry-run before anything runs.
	for _, p := range s.Ports {
		a = append(a, "-p", p)
	}

	// Container hardening. Order is fixed for deterministic output.
	if s.NoNewPrivileges {
		a = append(a, "--security-opt", "no-new-privileges")
	}
	if s.Seccomp != "" {
		a = append(a, "--security-opt", "seccomp="+s.Seccomp)
	}
	for _, c := range s.CapDrop {
		a = append(a, "--cap-drop", c)
	}
	for _, c := range s.CapAdd {
		a = append(a, "--cap-add", c)
	}
	if s.PidsLimit > 0 {
		a = append(a, "--pids-limit", strconv.FormatInt(s.PidsLimit, 10))
	}
	if s.Memory != "" {
		a = append(a, "--memory", s.Memory)
	}
	if s.CPUs != "" {
		a = append(a, "--cpus", s.CPUs)
	}

	if s.Entrypoint != "" {
		a = append(a, "--entrypoint", s.Entrypoint)
	}

	for _, m := range s.Mounts {
		kind := "bind"
		if m.Volume {
			kind = "volume"
		}
		mt := "type=" + kind + ",source=" + m.Source + ",target=" + m.Target
		if m.RO {
			mt += ",readonly"
		}
		a = append(a, "--mount", mt)
	}

	a = append(a, "-w", s.Workdir)
	if s.Home != "" {
		a = append(a, "-e", "HOME="+s.Home)
	}

	// Explicit key=value pairs, emitted in a stable order for deterministic output.
	for _, k := range sortedKeys(s.Env) {
		a = append(a, "-e", k+"="+s.Env[k])
	}
	// Pass-through by name: docker reads the host value at exec time.
	for _, k := range s.EnvNames {
		a = append(a, "-e", k)
	}

	a = append(a, s.Image)
	a = append(a, s.Command...)
	return a
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
