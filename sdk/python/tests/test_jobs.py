"""Tests the Python client's job, agent-run and secret methods against a
stand-in gateway.

    python3 -m unittest discover -s sdk/python/tests

A small http.server records each request and answers with canned JSON. It
needs no SANDBOXD binary.
"""

import base64
import json
import os
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from sandboxapi import ApiError, Client  # noqa: E402

JOB = {"id": "job_0123456789abcdef", "state": "running", "spec": {"command": ["echo", "hi"]},
       "created_at": "2026-10-04T00:00:00Z", "queued": 1, "running": 0, "succeeded": 0, "failed": 0,
       "runs": [{"n": 0, "state": "queued", "attempts": 0}]}

REPLIES = {
    ("POST", "/v1/jobs"): (201, JOB),
    ("POST", "/v1/agent-runs"): (201, JOB),
    ("GET", "/v1/jobs"): (200, {"jobs": [JOB]}),
    ("GET", "/v1/jobs/job_0123456789abcdef"): (200, JOB),
    ("DELETE", "/v1/jobs/job_0123456789abcdef"): (200, dict(JOB, state="cancelled")),
    ("GET", "/v1/jobs/job_0123456789abcdef/runs/0/output"): (200, {
        "stdout": base64.b64encode(b"hi\n").decode(), "stderr": None, "truncated": False}),
    ("GET", "/v1/jobs/job_0123456789abcdef/runs/0/files?path=%2Fsandbox%2Fhome%2Fr.txt"): (200, b"report"),
    ("PUT", "/v1/secrets/API_KEY"): (204, None),
    ("GET", "/v1/secrets"): (200, {"secrets": [{"name": "API_KEY", "updated_at": "2026-10-04T00:00:00Z"}]}),
    ("DELETE", "/v1/secrets/API_KEY"): (204, None),
}


class _Handler(BaseHTTPRequestHandler):
    def _serve(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n)) if n else None
        self.server.seen.append((self.command, self.path, self.headers.get("Authorization"), body))
        key = (self.command, self.path)
        ctype = "application/json"
        if key not in REPLIES:
            status, data = 404, json.dumps({"error": {"code": "not_found", "message": "no such job"}}).encode()
        else:
            status, reply = REPLIES[key]
            if isinstance(reply, bytes):
                data, ctype = reply, "application/octet-stream"
            else:
                data = b"" if reply is None else json.dumps(reply).encode()
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST = do_PUT = do_DELETE = _serve

    def log_message(self, *args):
        pass


class JobsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.srv = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        cls.srv.seen = []
        threading.Thread(target=cls.srv.serve_forever, daemon=True).start()
        cls.c = Client(f"http://127.0.0.1:{cls.srv.server_address[1]}", token="gw-key")

    @classmethod
    def tearDownClass(cls):
        cls.srv.shutdown()
        cls.srv.server_close()

    def last(self):
        return self.srv.seen[-1]

    def test_create_job(self):
        spec = {"command": ["echo", "hi"], "completions": 3, "parallelism": 2, "retries": 1,
                "keep": {"output": True, "files": ["/sandbox/home/r.txt"]}}
        j = self.c.create_job(spec)
        self.assertEqual(j["id"], "job_0123456789abcdef")
        self.assertEqual(self.last(), ("POST", "/v1/jobs", "Bearer gw-key", spec))

    def test_agent_run(self):
        self.c.agent_run("claude", "fix it", secrets=["ANTHROPIC_API_KEY"], timeout_secs=600)
        self.assertEqual(self.last()[:2], ("POST", "/v1/agent-runs"))
        self.assertEqual(self.last()[3], {"agent": "claude", "prompt": "fix it",
                                          "secrets": ["ANTHROPIC_API_KEY"], "timeout_secs": 600})

    def test_read_and_cancel(self):
        self.assertEqual([j["id"] for j in self.c.jobs()], ["job_0123456789abcdef"])
        self.assertEqual(self.c.job("job_0123456789abcdef")["runs"][0]["state"], "queued")
        self.assertEqual(self.c.cancel_job("job_0123456789abcdef")["state"], "cancelled")
        self.assertEqual(self.last()[:2], ("DELETE", "/v1/jobs/job_0123456789abcdef"))

    def test_output_and_files(self):
        out = self.c.job_output("job_0123456789abcdef", 0)
        self.assertEqual(out, {"stdout": b"hi\n", "stderr": b"", "truncated": False})
        self.assertEqual(self.c.job_file("job_0123456789abcdef", 0, "/sandbox/home/r.txt"), b"report")

    def test_secrets(self):
        self.c.set_secret("API_KEY", "sk-value")
        self.assertEqual(self.last(), ("PUT", "/v1/secrets/API_KEY", "Bearer gw-key", {"value": "sk-value"}))
        listed = self.c.secrets()
        self.assertEqual([s["name"] for s in listed], ["API_KEY"])
        self.assertNotIn("value", listed[0])
        self.c.delete_secret("API_KEY")
        self.assertEqual(self.last()[:2], ("DELETE", "/v1/secrets/API_KEY"))

    def test_someone_elses_job_is_not_found(self):
        with self.assertRaises(ApiError) as e:
            self.c.job("job_ffffffffffffffff")
        self.assertEqual(e.exception.code, "not_found")


if __name__ == "__main__":
    unittest.main()
