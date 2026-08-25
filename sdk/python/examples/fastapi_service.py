"""Serve a repository's FastAPI app in a sandbox, and reach it from here.

    python3 examples/fastapi_service.py                 # this repository, auto-detected
    python3 examples/fastapi_service.py my-api app.py   # a registered repo, a named module

It runs **the app the repository already has**. An earlier version of this file
cloned a repository, ignored everything in it, wrote an `app.py` on the fly and
served that — which demonstrated nothing about the repository and quietly implied
the clone had done some work. If no app is found, this one scaffolds a tiny app
and *says so*, because a fallback that looks like the real thing is the problem
that version had.

Cloning belongs in `python_project.py`, which is about bringing code in and
installing it. This example is about the part that file cannot show:

**A server never exits, so `run()` is the wrong verb.** `run()` waits, and
waiting on a server means reaching the deadline and then reporting a container
somebody stopped — a verdict on nothing. `start()` launches and returns the run;
the container outlives this script until something stops it.

**`publish` binds the port on the daemon's host**, which is why the health check
at the end can reach it — and why it only works when that host is this one.

**Configuration crosses as environment, per workspace.** `read_env_file` parses a
`.env` you name explicitly; `workspace(env=...)` applies it to every run there.
Values travel in the request body, so against a remote daemon without TLS they
cross in cleartext.

**The image has python3 and no pip**, deliberately — see
docs/proposals/python-in-the-image.md — so the setup builds a venv *without* pip
and bootstraps pip inside it, which needs three hosts named on the allowlist.
"""

from __future__ import annotations

import base64
import json
import sys
import time
import urllib.error
import urllib.request

from sandbox_cli import Studio
from sandbox_cli.env import read_env_file

BRANCH = "fastapi-demo"
PORT = 8123

# The hosts the setup needs, named rather than implied. `allow` adds to the
# daemon's posture and can never loosen it — and on a daemon whose egress is
# unrestricted, naming hosts turns the allowlist *on* for that run, so this is
# giving up the rest of the internet rather than asking for more of it.
SETUP_ALLOW = ["bootstrap.pypa.io", "pypi.org", "files.pythonhosted.org"]

# Where an app usually lives, in the order worth looking. Checked in the
# worktree, because that is where the run will look for it.
CANDIDATES = ["app.py", "main.py", "api.py", "src/main.py", "app/main.py"]

# The setup, once per worktree. The image ships python3 and no pip, so this is a
# venv *without* pip with pip bootstrapped inside it — `pip install` into the
# system interpreter is refused by PEP 668, correctly, even in a container.
SETUP = [
    ["python3", "-m", "venv", "--without-pip", ".venv"],
    # Into the worktree, not /tmp: the next step is a different container, and
    # only the worktree crosses that boundary.
    ["sh", "-c", "curl -fsSL https://bootstrap.pypa.io/get-pip.py -o get-pip.py"],
    ["sh", "-c", ".venv/bin/python3 get-pip.py -q && rm -f get-pip.py"],
    # The repository's own dependencies, when it has any. Serving its app means
    # its imports have to resolve, which is the whole difference between running
    # a repository's code and running code written into a repository.
    # `if`, not `A && B || true`: the latter binds as `(A && B) || true` and
    # swallows a *pip* failure as well as an absent file — after which uvicorn
    # dies on an import and the only symptom is a health check that times out.
    ["sh", "-c", "if [ -f requirements.txt ]; then .venv/bin/pip install -q -r requirements.txt; fi"],
    ["sh", "-c", "if [ -f setup.py ] || [ -f pyproject.toml ]; then .venv/bin/pip install -q -e .; fi"],
    # And the server itself, which the repository may not list because it is not
    # the repository's business how you run it.
    ["sh", "-c", ".venv/bin/pip install -q fastapi uvicorn"],
]

DEMO_APP = '''
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
    repo_name = sys.argv[1] if len(sys.argv) > 1 else None
    named_module = sys.argv[2] if len(sys.argv) > 2 else None

    studio = Studio.connect()
    repo = studio.project(repo_name) if repo_name else studio.project()

    # Configuration: a file you name, not one that is found for you.
    env = read_env_file(".env.demo", missing_ok=True) or {
        "SERVICE_NAME": "demo-api",
        "APP_ENV": "sandbox",
    }
    ws = repo.workspace(BRANCH, env=env)
    ws.clear_finished()

    module = named_module or find_app(ws)
    if module is None:
        # Said plainly rather than done quietly: nothing here is the
        # repository's, so nothing about it is being demonstrated.
        print("no FastAPI app found in this repository — scaffolding demo_app.py "
              f"(looked for: {', '.join(CANDIDATES)})")
        put_demo_app(ws)
        module = "demo_app.py"
    else:
        print(f"serving the repository's own app: {module}")

    print("setting up…")
    setup = ws.steps(SETUP, allow=SETUP_ALLOW, timeout=900)
    for i, step in enumerate(setup, 1):
        if step.exit_code != 0:
            print(f"step {i} failed ({step.exit_code}):\n{step.stderr}", file=sys.stderr)
            return step.exit_code
    print(f"  {len(setup)} steps ok")

    # `start`, not `run`: this is not meant to finish.
    target = uvicorn_target(module)
    run = ws.start(
        [".venv/bin/uvicorn", target, "--host", "0.0.0.0", "--port", str(PORT)],
        publish=[f"{PORT}:{PORT}"],
    )
    print(f"serving {target} as run {run['id'][:12]} on http://127.0.0.1:{PORT}")

    try:
        body = wait_for_health(f"http://127.0.0.1:{PORT}/health", ws, run["id"])
        print("health:", json.dumps(body))
        return 0
    finally:
        # Explicit, because nothing reaps a started run for you. Stopped rather
        # than removed: the logs are the evidence for what it did.
        print("stopping…")
        ws.stop(run["id"])


def uvicorn_target(module: str) -> str:
    """`app/main.py` -> `app.main:app`, and `app.main:api` -> itself.

    Blindly stripping three characters turned `main` into `n:app` and
    `app:app` into `a:app`, and the only symptom either way was a health check
    timing out twenty seconds later — a user typing what uvicorn itself takes
    got the least useful failure available.
    """
    if ":" in module:            # already a uvicorn target
        return module
    path = module[:-3] if module.endswith(".py") else module
    return path.strip("/").replace("/", ".") + ":app"


def find_app(ws) -> str | None:
    """The first candidate that exists *and* looks like a FastAPI app.

    Asked of the worktree rather than of this machine: the repository lives on
    the daemon's disk, and against a remote daemon this script cannot see it at
    all.
    """
    probe = " ; ".join(
        f'test -f {c} && grep -lq "FastAPI(" {c} && echo {c}' for c in CANDIDATES
    )
    found = ws.run(["sh", "-c", f"({probe}) 2>/dev/null | head -1"]).stdout.strip()
    return found or None


def put_demo_app(ws) -> None:
    """Write the fallback app, base64 so nothing in it can be shell."""
    b64 = base64.b64encode(DEMO_APP.encode()).decode()
    ws.run(["sh", "-c", f"printf %s '{b64}' | base64 -d > demo_app.py"])


def wait_for_health(url: str, ws, run_id: str, attempts: int = 40) -> dict:
    """Poll until the server answers.

    A published port is bound on the **daemon's** host, so this only works when
    that host is this one. On failure it prints the run's own output: a server
    that did not start has a reason, and it is in its logs rather than in the
    timeout.
    """
    last = ""
    for _ in range(attempts):
        try:
            with urllib.request.urlopen(url, timeout=2) as r:
                return json.load(r)
        except (urllib.error.URLError, OSError, ValueError) as e:
            last = str(e)
            time.sleep(0.5)

    tail = [l.get("text", "") for l in ws.logs(run_id)][-8:]
    raise RuntimeError(
        f"the server never answered on {url} ({last}).\nIts last output:\n  "
        + "\n  ".join(tail or ["(nothing)"])
    )


if __name__ == "__main__":
    raise SystemExit(main())
