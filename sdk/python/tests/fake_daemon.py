"""A stand-in daemon: enough of the contract to pin this client's rules.

Modelled on the TypeScript suite's fake, including the one behaviour that matters
most — a finished run keeps its branch's container name, so the *next* launch is
refused until it is removed. That is the rule every sequential script hits.
"""

from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer


class FakeDaemon:
    def __init__(self, *, holds_name_after_run: bool = False, project_root: str = "/repo/app",
                 fail_after: int | None = None, never_finishes: bool = False,
                 failover: bool = False):
        self.requests: list[dict] = []
        self.removed: list[str] = []
        self.holds_name_after_run = holds_name_after_run
        self._fail_after = fail_after
        self.never_finishes = never_finishes
        self.failover = failover
        self._launches = 0
        self.project_root = project_root
        self._held = ""
        self._server = HTTPServer(("127.0.0.1", 0), _handler(self))
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    def __enter__(self) -> "FakeDaemon":
        self._thread.start()
        return self

    def __exit__(self, *_exc) -> None:
        self._server.shutdown()
        self._server.server_close()

    @property
    def url(self) -> str:
        host, port = self._server.server_address[:2]
        return f"http://{host}:{port}"

    def posted(self, path: str) -> list[dict]:
        return [r for r in self.requests if r["method"] == "POST" and r["path"] == path]


def _handler(state: FakeDaemon):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):  # keep the test output readable
            pass

        def _send(self, code: int, body):
            raw = json.dumps(body).encode() if body is not None else b""
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            if raw:
                self.wfile.write(raw)

        def _record(self):
            length = int(self.headers.get("content-length") or 0)
            body = json.loads(self.rfile.read(length)) if length else None
            state.requests.append({"method": self.command, "path": self.path,
                                   "body": body, "auth": self.headers.get("Authorization")})
            return body

        def do_GET(self):  # noqa: N802
            self._record()
            if self.path == "/v1/health":
                return self._send(200, {"status": "ok", "engine": "docker", "profile": "dev"})
            if self.path == "/v1/projects":
                return self._send(200, {"projects": [
                    {"id": "repo-1", "name": "app", "root": state.project_root},
                    {"id": "repo-2", "name": "twin", "root": "/a/twin"},
                    {"id": "repo-3", "name": "twin", "root": "/b/twin"}]})
            if self.path.startswith("/v1/runs/") and self.path.endswith("/logs"):
                return self._send(200, [{"seq": 0, "ts": "", "stream": "stdout", "text": "hi"}])
            if state.failover and self.path == "/v1/runs?all=1&repo=repo-1":
                # The daemon stamps routedFrom on the replacement it started.
                return self._send(200, {"runs": [
                    {"id": "run-2", "state": "exited", "exitCode": 0, "agent": "codex",
                     "routedFrom": "run-1", "branch": "feature", "repoId": "repo-1"}]})
            if state.failover and self.path == "/v1/runs/run-2":
                return self._send(200, {"id": "run-2", "state": "exited", "exitCode": 0,
                                        "agent": "codex", "routedFrom": "run-1",
                                        "branch": "feature", "repoId": "repo-1"})
            if state.never_finishes and self.path.startswith("/v1/runs/") and \
                    not self.path.endswith("/logs"):
                return self._send(200, {"id": "run-1", "state": "running", "branch": "feature"})
            if self.path.startswith("/v1/runs/"):
                failing = (state._fail_after is not None
                           and state._launches > state._fail_after)
                return self._send(200, {"id": "run-1", "state": "exited",
                                        "exitCode": 1 if (failing or state.failover) else 0,
                                        "agent": "claude" if state.failover else None,
                                        "branch": "feature", "repoId": "repo-1"})
            if self.path.startswith("/v1/runs"):
                return self._send(200, {"runs": []})
            return self._send(404, {"error": "no such thing"})

        def do_POST(self):  # noqa: N802
            body = self._record()
            if self.path == "/v1/worktrees":
                return self._send(201, {"branch": body["branch"], "repoId": "repo-1"})
            if self.path == "/v1/projects":
                path = (body or {}).get("path", "")
                if not path.startswith("/"):
                    return self._send(422, {"error": f"{path} is not an absolute path"})
                return self._send(201, {"id": "repo-4", "name": path.rstrip("/").split("/")[-1],
                                        "root": path})
            if self.path == "/v1/projects/clone":
                url = (body or {}).get("url", "")
                return self._send(201, {"id": "repo-9", "name": url.rstrip("/").split("/")[-1],
                                        "root": "/cloned/here"})
            if self.path == "/v1/runs":
                state._launches += 1
                if state._held:
                    return self._send(409, {"error": (
                        f'a finished run ({state._held}, exit 0) still holds "feature"\'s '
                        f"container name; read it with GET /v1/runs/{state._held}/logs, then "
                        f"DELETE /v1/runs/{state._held} to run again")})
                if state.holds_name_after_run:
                    state._held = "run-1"
                return self._send(201, {"id": "run-1", "state": "running", "branch": "feature"})
            if self.path.endswith("/stop"):
                return self._send(204, None)
            return self._send(404, {"error": "no such thing"})

        def do_DELETE(self):  # noqa: N802
            self._record()
            if self.path.startswith("/v1/runs/"):
                run_id = self.path.rsplit("/", 1)[-1]
                state.removed.append(run_id)
                if state._held == run_id:
                    state._held = ""
                return self._send(204, None)
            return self._send(404, {"error": "no such thing"})

    return Handler
