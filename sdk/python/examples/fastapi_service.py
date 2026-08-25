"""Clone a repository, install its dependencies, and run its FastAPI server.

The whole shape of a real setup in one file: bring code in, give it
configuration, build an environment, then start something that is not supposed
to stop — and reach it from here.

    python3 examples/fastapi_service.py

Four things it demonstrates, each of which is a constraint somewhere:

**Cloning happens on the daemon's machine.** `clone` takes a git URL or the
GitHub shorthand `owner/repo`, and the directory it clones into belongs to that
host — not to the machine running this script.

**Configuration crosses as environment, per workspace.** `read_env_file` parses
a `.env` you name explicitly, and `workspace(env=...)` applies it to every run in
that branch. Values travel in the request body, so against a remote daemon
without TLS they cross in cleartext; keep production secrets out of demos.

**The image has python3 and no pip.** Deliberately — see
docs/proposals/python-in-the-image.md. So the setup builds a venv *without* pip
and bootstraps pip inside it, which needs three hosts on the egress allowlist.
That is not a workaround to hide; it is what the sandbox costs today, and naming
the hosts is how you decide whether you are comfortable with it.

**A server never exits, so `run()` is the wrong verb.** `start()` launches and
returns the run; the container outlives this script until something stops it.
`publish` binds the port on the daemon's host, which is why the health check at
the end can reach it.
"""

from __future__ import annotations

import json
import sys
import time
import urllib.error
import urllib.request

from sandbox_cli import Studio
from sandbox_cli.env import read_env_file

REPO = "Amitgb14/sandbox-cli"       # any git URL, or owner/repo for GitHub
CLONE_PARENT = "/tmp"               # must already exist on the daemon's machine
BRANCH = "fastapi-demo"
PORT = 8123

# The hosts the setup needs, named rather than implied. `allow` adds to the
# daemon's posture and can never loosen it — and on a daemon whose egress is
# unrestricted, naming hosts turns the allowlist *on* for that run, so this is
# giving up the rest of the internet rather than asking for more of it.
SETUP_ALLOW = ["bootstrap.pypa.io", "pypi.org", "files.pythonhosted.org"]

APP = '''
import os
from fastapi import FastAPI

app = FastAPI()

@app.get("/health")
def health():
    # Reads configuration that came from the .env on the host, to prove it
    # arrived rather than to do anything useful with it.
    return {"ok": True, "service": os.environ.get("SERVICE_NAME", "unset"),
            "env": os.environ.get("APP_ENV", "unset")}
'''


def main() -> int:
    studio = Studio.connect()

    # 1. The code. Cloned by the daemon, onto the daemon's disk.
    # `parent` must already exist on that machine — the daemon clones into it and
    # will not create it, and this client has no endpoint that could. /tmp is the
    # one directory it is safe to assume.
    try:
        repo = studio.clone(REPO, parent=CLONE_PARENT)
    except Exception as refused:  # noqa: BLE001 — the daemon's refusal is the message
        print(f"clone: {refused}", file=sys.stderr)
        print(f"The parent must exist on the daemon's machine. If {REPO} is already "
              f"cloned, use studio.add_project(<path>) instead.", file=sys.stderr)
        return 1

    # 2. The configuration. A file you name, not one that is found for you.
    env = read_env_file(".env.demo", missing_ok=True) or {
        "SERVICE_NAME": "demo-api",
        "APP_ENV": "sandbox",
    }
    ws = repo.workspace(BRANCH, env=env)
    ws.clear_finished()

    # 3. The environment. A venv *without* pip, then pip bootstrapped inside it:
    # the image ships python3 and no pip, and installing into the system
    # interpreter is refused by PEP 668 — correctly, even in a container.
    print("setting up…")
    setup = ws.steps([
        ["sh", "-c", f"cat > app.py <<'EOF'\n{APP}\nEOF"],
        ["python3", "-m", "venv", "--without-pip", ".venv"],
        # Into the worktree, not /tmp: each step is a **new container**, and only
        # the worktree survives between them. A file left in /tmp by one step is
        # gone by the next, which fails several steps later as "No such file".
        ["sh", "-c", "curl -fsSL https://bootstrap.pypa.io/get-pip.py -o get-pip.py"],
        ["sh", "-c", ".venv/bin/python3 get-pip.py -q && rm -f get-pip.py"],
        ["sh", "-c", ".venv/bin/pip install -q fastapi uvicorn"],
    ], allow=SETUP_ALLOW, timeout=600)

    for i, step in enumerate(setup, 1):
        if step.exit_code != 0:
            print(f"step {i} failed ({step.exit_code}):\n{step.stderr}", file=sys.stderr)
            return step.exit_code
    print(f"  {len(setup)} steps ok")

    # 4. The server. `start`, not `run`: this is not meant to finish.
    run = ws.start(
        [".venv/bin/uvicorn", "app:app", "--host", "0.0.0.0", "--port", str(PORT)],
        publish=[f"{PORT}:{PORT}"],
        allow=SETUP_ALLOW,
    )
    print(f"serving as run {run['id'][:12]} on http://127.0.0.1:{PORT}")

    try:
        body = wait_for_health(f"http://127.0.0.1:{PORT}/health")
        print("health:", json.dumps(body))
        # The configuration came from the .env, through the workspace, into the
        # process. That is the whole chain this example exists to show.
        assert body.get("service") == env.get("SERVICE_NAME")
        return 0
    finally:
        # Explicit, because nothing reaps a started run for you. Stopped rather
        # than removed: the logs are the evidence for what it did.
        print("stopping…")
        ws.stop(run["id"])


def wait_for_health(url: str, attempts: int = 40) -> dict:
    """Poll until the server answers. A published port is bound on the daemon's
    host, so this only works when that host is this one."""
    last = ""
    for _ in range(attempts):
        try:
            with urllib.request.urlopen(url, timeout=2) as r:
                return json.load(r)
        except (urllib.error.URLError, OSError, ValueError) as e:
            last = str(e)
            time.sleep(0.5)
    raise RuntimeError(f"the server never answered on {url}: {last}")


if __name__ == "__main__":
    raise SystemExit(main())
