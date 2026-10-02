// Package creds is the credential broker. It resolves secret *references* — a
// host file, a host command, or a host environment variable — into concrete
// environment values at run time. sandbox-cli forwards those values into the
// container by name (docker `-e NAME`), so the raw secret never appears on the
// docker command line, in `--dry-run` output, in a config file, or in shell
// history, and command-sourced secrets (e.g. `op read`, `gh auth token`,
// `vault read`) can be short-lived and fetched fresh each run.
//
// Scope note: the agent process inside the container still receives the value as
// an environment variable (it needs it to authenticate). This broker closes the
// "secret on the host command line / in config" gap, not the "agent never sees
// the key" one — and the second gap is now one the tool has decided **not** to
// close that way. Hiding the value from the agent entirely needs a
// header-injecting egress proxy (as in Docker `sbx`), which requires terminating
// TLS, which requires a CA in the container: one process would then hold every
// token, every prompt in plaintext and the CA private key. Rejected 2026-08-04
// on blast radius; see open-items.md item 2.
//
// What was adopted instead is to make a leak cheap rather than impossible —
// prod's credential-free container, short-lived brokered values, one credential
// per project, a short allowlist. lifetime.go is the only part of that needing
// code: it is what stops "brokered" being assumed of a credential that lasts
// months.
package creds

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// EnvVar is a resolved environment variable to inject into the container.
type EnvVar struct {
	Name  string
	Value string
}

// Source describes where a secret's value comes from. Exactly one field is set;
// Resolve enforces that.
type Source struct {
	File    string // read the value from this host file (whitespace-trimmed)
	Command string // run this host command via `sh -c`; trimmed stdout is the value
	Env     string // read the value from this host environment variable
}

// Resolve fetches every secret to a concrete env var, in sorted name order for
// deterministic behavior. It performs I/O (reads files, runs commands) and must
// be called only on the real run path — never for --dry-run, so secrets are not
// read or executed merely to print the docker command.
func Resolve(secrets map[string]Source) ([]EnvVar, error) {
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]EnvVar, 0, len(names))
	for _, name := range names {
		v, err := resolveOne(name, secrets[name])
		if err != nil {
			return nil, err
		}
		out = append(out, EnvVar{Name: name, Value: v})
	}
	return out, nil
}

func resolveOne(name string, s Source) (string, error) {
	switch {
	case s.File != "":
		b, err := os.ReadFile(s.File)
		if err != nil {
			return "", fmt.Errorf("secret %q: reading file: %w", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	case s.Command != "":
		cmd := exec.Command("sh", "-c", s.Command)
		cmd.Stderr = os.Stderr
		b, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("secret %q: running command: %w", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	case s.Env != "":
		v, ok := os.LookupEnv(s.Env)
		if !ok {
			return "", fmt.Errorf("secret %q: host env %q is not set", name, s.Env)
		}
		return v, nil
	default:
		return "", fmt.Errorf("secret %q: no source (set exactly one of file, command, or env)", name)
	}
}
