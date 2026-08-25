"""Install a repository's dependencies once, then run its scripts.

The everyday shape: a repo with several Python scripts and a requirements.txt,
where the install should happen on the first run and never again.

    python3 examples/python_project.py pyqualys                       # setup, then the tests
    python3 examples/python_project.py pyqualys example/example_report.py

**The virtualenv lives in the worktree, which is what makes this cheap.** Each
run is a new container and nothing outside `/workspace` survives it — but the
worktree does, so `.venv/` built by one run is there for the next. The setup is
skipped when it is already present, which turns a two-minute first run into a
one-second second one.

**The image ships python3 and no pip**, deliberately (see
docs/proposals/python-in-the-image.md), so the setup builds a venv *without* pip
and bootstraps pip inside it. `pip install` into the system interpreter is
refused by PEP 668, correctly, even in a container. Three hosts have to be named
on the egress allowlist for that, and they are named here rather than assumed.

**Nothing is installed on your machine.** The dependencies land in a container's
view of a worktree the daemon owns; your interpreter never sees them.
"""

from __future__ import annotations

import sys

from sandbox_cli import Studio

# The hosts the install needs. `allow` adds to the daemon's posture and can never
# loosen it — and naming hosts turns the allowlist *on* for a run whose daemon
# had none, so this trades the rest of the internet for these three.
PIP_HOSTS = ["bootstrap.pypa.io", "pypi.org", "files.pythonhosted.org"]

# Hosts the *script* needs, as opposed to the install. Empty by default, because
# most scripts need nothing and a run that asks for no egress gets none. Add the
# API your script talks to — e.g. ["qualysapi.qualys.com"] — and nothing else
# becomes reachable by doing so.
SCRIPT_ALLOW: list[str] = []

SETUP = [
    ["python3", "-m", "venv", "--without-pip", ".venv"],
    # Into the worktree, not /tmp: the next step is a different container, and
    # only the worktree crosses that boundary.
    ["sh", "-c", "curl -fsSL https://bootstrap.pypa.io/get-pip.py -o get-pip.py"],
    ["sh", "-c", ".venv/bin/python3 get-pip.py -q && rm -f get-pip.py"],
    # Guarded, and with the install's own status preserved: `A && B || true`
    # binds as `(A && B) || true`, so it swallows a *pip* failure as well as an
    # absent file, and the first symptom is an import error much later.
    ["sh", "-c", "if [ -f requirements.txt ]; then .venv/bin/pip install -q -r requirements.txt; fi"],
    # If the repository is itself a package, install it too. Without this, its
    # own scripts fail with `No module named <the repo>` — they import the
    # package they live beside, which only resolves when it is on the path.
    # `|| true` because plenty of repositories are a folder of scripts with no
    # setup.py, and that is not a failure.
    ["sh", "-c", "if [ -f setup.py ] || [ -f pyproject.toml ]; then .venv/bin/pip install -q -e .; fi"],
]


def main() -> int:
    repo_name = sys.argv[1] if len(sys.argv) > 1 else "my-app"
    script = sys.argv[2] if len(sys.argv) > 2 else None

    studio = Studio.connect()
    repo = studio.project(repo_name)
    ws = repo.workspace("deps")
    # A finished run keeps its branch's container name, and this script is a new
    # process every time — so the run it left behind last time is not one it may
    # clear implicitly. This is the line that makes it re-runnable.
    ws.clear_finished()

    if not venv_present(ws):
        print("installing dependencies (first run only)…")
        for i, step in enumerate(ws.steps(SETUP, allow=PIP_HOSTS, timeout=900), 1):
            if step.exit_code != 0:
                print(f"setup step {i} failed ({step.exit_code}):\n{step.stderr}", file=sys.stderr)
                # The commonest cause, and worth naming rather than leaving to a
                # traceback: the container could not reach the package index.
                print(f"\nIf that is a network error, the run needs {', '.join(PIP_HOSTS)} "
                      f"on the allowlist.", file=sys.stderr)
                return step.exit_code
        print("  installed")
    else:
        print("dependencies already present in the worktree — skipping setup")

    # Whatever the repository already has. The install hosts are *not* passed
    # here: pip is done, and a run that does not ask for egress does not get it.
    #
    # A script that talks to an API needs its host named — SCRIPT_ALLOW below.
    # Running this against pyqualys' example_report.py made a real request and
    # got a 403 back from the vendor's edge, which is the right kind of failure:
    # the code ran, the network was reachable because this daemon allows it, and
    # the only thing missing was a credential. On an allowlist daemon the same
    # script fails earlier and more clearly, at the connection.
    # Not piped through `tail`. POSIX sh has no pipefail, so a pipeline's status
    # is the *last* command's — `tail` always succeeds, and a red test suite
    # would have been reported as exit 0 by an example whose whole job is saying
    # what happened. The output is trimmed here instead, where trimming cannot
    # change a verdict.
    argv = ([".venv/bin/python3", script] if script
            else [".venv/bin/python3", "-m", "unittest", "discover", "-s", ".", "-t", "."])
    out = ws.run(argv, timeout=900, **({"allow": SCRIPT_ALLOW} if SCRIPT_ALLOW else {}))
    # unittest writes its report to stderr, and a script may use either.
    report = (out.stdout.rstrip() + "\n" + out.stderr.rstrip()).strip()
    print(tail(report, 12))
    print(f"exit {out.exit_code}")
    return out.exit_code


def tail(text: str, lines: int) -> str:
    """The last few lines, trimmed here rather than in the container — where a
    pipe would have replaced the run's exit code with `tail`'s."""
    kept = text.splitlines()[-lines:]
    return "\n".join(kept)


def venv_present(ws) -> bool:
    """Ask the worktree, not a flag in this process.

    The state that matters is on the daemon's disk, and it outlives this script —
    so a variable here would be wrong the moment somebody runs the script twice,
    or deletes the worktree, or another script sets it up first.
    """
    return ws.run(["sh", "-c", "test -x .venv/bin/python3 && echo yes || echo no"]).stdout.strip() == "yes"


if __name__ == "__main__":
    raise SystemExit(main())
