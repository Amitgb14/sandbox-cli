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
		// relabel=shared is the --mount spelling of :z. Without it SELinux denies
		// the mount outright on Fedora/RHEL/CentOS, so the agent cannot even read
		// its own workspace. "shared" rather than "private" because the user works
		// in that directory too, and a private label would lock them out of it.
		if !m.Volume && s.HostUserMapping != "" {
			mt += ",relabel=shared"
		}
		a = append(a, "--mount", mt)
	}

	// Placed after the mounts so the relabel and the mapping read together.
	if s.HostUserMapping != "" {
		a = append(a, "--userns="+s.HostUserMapping)
	}

	a = append(a, "-w", s.Workdir)
	if s.Home != "" {
		a = append(a, "-e", "HOME="+s.Home)
	}

	// Pass-through by name first, then explicit key=value pairs (stable order for
	// deterministic output). The order is load-bearing, not cosmetic: docker keeps
	// the LAST occurrence of a repeated -e, so rendering the by-name forwards last
	// would let a forwarded host variable overwrite a value BuildSpec set
	// deliberately. That was reachable — forwarding SANDBOX_RUN_AS with `root` set
	// on the host made the firewall entrypoint skip its privilege drop and run the
	// agent as root. collidingNames below is the primary guard; this ordering is
	// the backstop, so a name that slips past it still loses to the explicit value.
	for _, k := range s.EnvNames {
		if _, explicit := s.Env[k]; explicit {
			continue // an explicit value wins; forwarding the name too is a no-op at best
		}
		a = append(a, "-e", k)
	}
	for _, k := range sortedKeys(s.Env) {
		a = append(a, "-e", k+"="+s.Env[k])
	}

	// `--` before the image: `image` is a config-supplied string, and without the
	// separator a value beginning with a dash is read by docker as another flag
	// (`image: "--privileged"` rendered as a real --privileged, with the guest's
	// first argument silently becoming the image). config.Validate rejects that
	// shape now; this makes the argv unambiguous regardless.
	a = append(a, "--", s.Image)
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
