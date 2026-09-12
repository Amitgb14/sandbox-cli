package sandbox

import (
	"fmt"
	"strings"
)

// The verify wrapper, which lives here because three packages launch a container
// the same way a fleet task does and one of them cannot reach `internal/fleet`.
//
// It started in `internal/fleet`, was exported once for `internal/studioapi`, and
// moved when `internal/session` became the third caller — `session` is imported *by*
// fleet, so reaching back for it would be an import cycle, and the alternative was a
// second copy. A second copy of this in particular is the thing not to have: the exit
// code below is a contract `fleet status` and `fleet land` read back off a container
// that may have exited days ago, and the subshell around the verify is a bug fix that
// took a real failure to find. `internal/fleet` keeps both names as aliases, so every
// existing call site is unchanged and there is still one implementation.

// VerifyFailedExit is the container exit code meaning "the agent finished, and the
// task's own verify command said the work is not done".
//
// It has to be a code the agent itself will not plausibly produce, or land cannot
// tell the two apart: 90 sits above the usual application range and below the
// shell's reserved 126/127/128+n. This number is a contract — `fleet status` and
// `fleet land` both read it back off a container that may have exited days ago —
// so it is a constant, and a test pins it.
const VerifyFailedExit = 90

// verifyScript wraps an agent argv so the container runs the verify command
// after the agent and exits with the verdict.
//
// Why in the container rather than on the host: the host may not have the
// toolchain, and more to the point a verification command that runs on the host is
// host code selected by a file the agent can write. Why in the *same* container
// rather than a second one: a container whose PID 1 has exited is exited, so there
// is no `docker exec` left to make — and a second container would be a second
// spec, which is exactly the invariant surface this must not grow.
//
// The verdict is the exit code, and nothing else, because the exit code is the one
// thing that survives with the container. Docker is the state store.
//
// Verify runs whatever the agent's own exit code was, on purpose. An agent that
// exits non-zero having left a working tree that builds and tests clean has done
// the job; an agent that exits 0 having deleted the test file has not. The
// verify command is the definition of done — that is the whole point of the field
// — so it, not the agent, gets the last word. Both codes are printed so `fleet
// logs` still shows what the agent thought.
//
// The verify runs in a subshell, and that is load-bearing rather than tidy. It
// used to be interpolated into this script directly, which shares one shell with
// the mapping below — so a verify containing `exit 1`, the obvious way to write
// `test -d build || { echo …; exit 1; }`, terminated the wrapper before it could
// turn the result into VerifyFailedExit. The container then exited 1, and 1 is
// the code for "died before its verify ran": a check that ran and correctly said
// no was reported as a check that never happened, which `fleet status` shows as
// `unchecked` and `land` refuses with the wrong reason.
//
// `set -e` had the same effect for free, and it is the first line of any careful
// verify. Parentheses contain both: `exit` leaves the subshell, its status lands
// in $?, and the mapping below is the only thing that decides the container's
// exit code.
const verifyScript = `"$@"
sandbox_agent_rc=$?
echo "sandbox-cli: agent exited $sandbox_agent_rc; running verify" >&2
(
%s
)
sandbox_verify_rc=$?
if [ "$sandbox_verify_rc" -eq 0 ]; then
  echo "sandbox-cli: verify passed" >&2
  exit 0
fi
echo "sandbox-cli: verify failed (exit $sandbox_verify_rc)" >&2
exit %d`

// WithVerify returns the container argv for argv followed by verify, or argv
// unchanged when there is no verify.
//
// The agent's argv is passed through "$@" rather than pasted into the script: it
// carries the prompt, which is free text a user wrote and may contain quotes,
// newlines and $ — interpolating it would make the prompt able to rewrite the
// script that judges it.
func WithVerify(argv []string, verify string) []string {
	verify = strings.TrimSpace(verify)
	if verify == "" {
		return argv
	}
	script := fmt.Sprintf(verifyScript, verify, VerifyFailedExit)
	// "sh" is $0 for the wrapper, so the agent's own argv lands in "$@" whole.
	return append([]string{"sh", "-c", script, "sh"}, argv...)
}
