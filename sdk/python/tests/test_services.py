"""Tests the Python client's service methods against a stand-in gateway.

    python3 -m unittest discover -s sdk/python/tests

The stand-in is a small http.server that keeps services by name and answers
as a gateway does. It needs no SANDBOXD binary.
"""

import json
import os
import sys
import threading
import unittest
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from sandboxapi import ApiError, Client  # noqa: E402


class _Handler(BaseHTTPRequestHandler):
    def _reply(self, status, body=None):
        data = b"" if body is None else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _serve(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n)) if n else None
        srv = self.server
        srv.seen.append((self.command, self.path, self.headers.get("Authorization"), body))
        path = self.path
        if path == "/v1/services" and self.command == "POST":
            if body["name"] in srv.svcs:
                return self._reply(409, {"error": {"code": "conflict", "message": "exists"}})
            srv.svcs[body["name"]] = {"spec": body, "revision": 1, "desired": body.get("replicas", 0),
                                      "ready": 0, "replicas": []}
            return self._reply(201, srv.svcs[body["name"]])
        if path == "/v1/services" and self.command == "GET":
            return self._reply(200, {"services": list(srv.svcs.values())})
        if path.startswith("/v1/services/"):
            rest = path[len("/v1/services/"):]
            raw_name, _, action = rest.partition("/")
            name = urllib.parse.unquote(raw_name)
            svc = srv.svcs.get(name)
            if svc is None:
                return self._reply(404, {"error": {"code": "not_found", "message": "no such service"}})
            if self.command == "GET" and not action:
                return self._reply(200, svc)
            if self.command == "PUT" and not action:
                svc["spec"] = body
                svc["revision"] += 1
                svc["rollout"] = {"state": "in_progress", "from": svc["revision"] - 1, "to": svc["revision"]}
                return self._reply(200, svc)
            if self.command == "POST" and action == "scale":
                svc["desired"] = body["replicas"]
                return self._reply(200, svc)
            if self.command == "DELETE" and not action:
                del srv.svcs[name]
                return self._reply(204)
        return self._reply(404, {"error": {"code": "not_found", "message": "no such endpoint"}})

    do_GET = do_POST = do_PUT = do_DELETE = _serve

    def log_message(self, *args):
        pass


class ServicesTest(unittest.TestCase):
    def setUp(self):
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        self.srv.seen = []
        self.srv.svcs = {}
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.c = Client(f"http://127.0.0.1:{self.srv.server_address[1]}", token="gw-key")

    def tearDown(self):
        self.srv.shutdown()
        self.srv.server_close()

    def last(self):
        return self.srv.seen[-1]

    def test_lifecycle(self):
        spec = {"name": "web", "image": "img:1", "replicas": 2, "port": 8080,
                "health": {"http": "/healthz", "every_secs": 10}, "public": True}
        s = self.c.deploy_service(spec)
        self.assertEqual(s["revision"], 1)
        self.assertEqual(self.last(), ("POST", "/v1/services", "Bearer gw-key", spec))
        with self.assertRaises(ApiError) as e:
            self.c.deploy_service(spec)
        self.assertEqual(e.exception.code, "conflict")

        self.assertEqual([x["spec"]["name"] for x in self.c.services()], ["web"])
        self.assertEqual(self.c.service("web")["desired"], 2)
        self.assertEqual(self.last()[:2], ("GET", "/v1/services/web"))

        spec["image"] = "img:2"
        s = self.c.update_service(spec)
        self.assertEqual(s["rollout"]["state"], "in_progress")
        self.assertEqual(self.last()[:2], ("PUT", "/v1/services/web"))

        self.assertEqual(self.c.scale_service("web", 5)["desired"], 5)
        self.assertEqual(self.last(), ("POST", "/v1/services/web/scale", "Bearer gw-key", {"replicas": 5}))

        self.assertIsNone(self.c.delete_service("web"))
        self.assertEqual(self.last()[:2], ("DELETE", "/v1/services/web"))
        with self.assertRaises(ApiError) as e:
            self.c.service("web")
        self.assertEqual(e.exception.code, "not_found")

    def test_names_are_escaped(self):
        with self.assertRaises(ApiError):
            self.c.service("a/b")
        self.assertEqual(self.last()[1], "/v1/services/a%2Fb")


if __name__ == "__main__":
    unittest.main()
