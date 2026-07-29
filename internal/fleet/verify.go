package fleet

import (
	"fmt"
	"strings"
)

// VerifyFailedExit is the container exit code meaning "the agent finished, and the
// task's own verify command said the work is not done".
//
// It has to be a code the agent itself will not plausibly produce, or land cannot
// tell the two apart: 90 sits above the usual application range and below the
// shell's reserved 126/127/128+n. This number is a contract — `fleet status` and
// `fleet land` both read it back off a container that may have exited days ago —
// so it is a constant, and a test pins it.
const VerifyFailedExit = 90

// verifyScript wraps a task's agent argv so the container runs the verify command
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
// the job; an agent that exits 0 having deleted the test file has not. The task's
// verify command is the definition of done — that is the whole point of the field
// — so it, not the agent, gets the last word. Both codes are printed so `fleet
// logs` still shows what the agent thought.
const verifyScript = `"$@"
sandbox_agent_rc=$?
echo "sandbox-cli: agent exited $sandbox_agent_rc; running verify" >&2
%s
sandbox_verify_rc=$?
if [ "$sandbox_verify_rc" -eq 0 ]; then
  echo "sandbox-cli: verify passed" >&2
  exit 0
fi
echo "sandbox-cli: verify failed (exit $sandbox_verify_rc)" >&2
exit %d`

// withVerify returns the container argv for argv followed by verify, or argv
// unchanged when the task declared no verify.
//
// The agent's argv is passed through "$@" rather than pasted into the script: it
// carries the prompt, which is free text a user wrote and may contain quotes,
// newlines and $ — interpolating it would make the task's own prompt able to
// rewrite the script that judges it.
func withVerify(argv []string, verify string) []string {
	verify = strings.TrimSpace(verify)
	if verify == "" {
		return argv
	}
	script := fmt.Sprintf(verifyScript, verify, VerifyFailedExit)
	// "sh" is $0 for the wrapper, so the agent's own argv lands in "$@" whole.
	return append([]string{"sh", "-c", script, "sh"}, argv...)
}
