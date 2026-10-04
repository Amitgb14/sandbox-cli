"""Tests the Python client's gateway methods against a stand-in gateway.

    python3 -m unittest discover -s sdk/python/tests

A sandboxd answers none of these endpoints, so the stand-in is a small
http.server that records each request and answers with canned JSON. It needs
no SANDBOXD binary.
"""

import json
import os
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from sandboxapi import ApiError, Client  # noqa: E402

REPLIES = {
    ("GET", "/v1/whoami"): {"user": "ana", "tenant": "t1", "key_id": "key_1", "scopes": ["sandbox:ssh"]},
    ("GET", "/v1/ssh"): {"host": "gw.example", "port": 2222, "host_keys": ["ssh-ed25519 AAAA"],
                         "fingerprint": "SHA256:x"},
    ("POST", "/v1/ssh-keys"): {"id": "sk_1", "fingerprint": "SHA256:k", "key": "ssh-ed25519 AAAA me",
                               "created": "2026-10-04T00:00:00Z"},
    ("GET", "/v1/ssh-keys"): {"keys": [{"id": "sk_1", "fingerprint": "SHA256:k", "key": "ssh-ed25519 AAAA me",
                                        "created": "2026-10-04T00:00:00Z"}]},
    ("DELETE", "/v1/ssh-keys/sk%2F1"): None,
    ("POST", "/v1/sandboxes/demo/ssh-access"): {"user": "tok", "host": "gw.example", "port": 2222,
                                                "expires_at": "2026-10-04T00:15:00Z",
                                                "command": "ssh -p 2222 tok@gw.example"},
}


class _Handler(BaseHTTPRequestHandler):
    def _serve(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n)) if n else None
        self.server.seen.append((self.command, self.path, self.headers.get("Authorization"), body))
        key = (self.command, self.path)
        if key not in REPLIES:
            data = json.dumps({"error": {"code": "not_found", "message": "no such endpoint"}}).encode()
            self.send_response(404)
        else:
            reply = REPLIES[key]
            data = b"" if reply is None else json.dumps(reply).encode()
            self.send_response(200 if data else 204)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST = do_DELETE = _serve

    def log_message(self, *args):
        pass


class GatewayTest(unittest.TestCase):
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

    def test_whoami(self):
        self.assertEqual(self.c.whoami()["user"], "ana")
        self.assertEqual(self.last(), ("GET", "/v1/whoami", "Bearer gw-key", None))

    def test_ssh_info(self):
        self.assertEqual(self.c.ssh_info()["port"], 2222)
        self.assertEqual(self.last()[:2], ("GET", "/v1/ssh"))

    def test_ssh_keys(self):
        self.assertEqual(self.c.add_ssh_key("ssh-ed25519 AAAA me")["id"], "sk_1")
        self.assertEqual(self.last(), ("POST", "/v1/ssh-keys", "Bearer gw-key", {"key": "ssh-ed25519 AAAA me"}))
        self.c.add_ssh_key("ssh-ed25519 AAAA me", sandbox="demo")
        self.assertEqual(self.last()[3], {"key": "ssh-ed25519 AAAA me", "sandbox": "demo"})
        self.assertEqual([k["id"] for k in self.c.ssh_keys()], ["sk_1"])
        self.c.remove_ssh_key("sk/1")
        self.assertEqual(self.last()[:2], ("DELETE", "/v1/ssh-keys/sk%2F1"))

    def test_ssh_access(self):
        a = self.c.ssh_access("demo", ttl_secs=900)
        self.assertEqual(a["command"], "ssh -p 2222 tok@gw.example")
        self.assertEqual(self.last(), ("POST", "/v1/sandboxes/demo/ssh-access", "Bearer gw-key", {"ttl_secs": 900}))
        self.c.ssh_access("demo")
        self.assertEqual(self.last()[3], {})

    def test_a_plain_sandboxd_answers_not_found(self):
        with self.assertRaises(ApiError) as e:
            self.c.ssh_access("other")
        self.assertEqual(e.exception.code, "not_found")


if __name__ == "__main__":
    unittest.main()
