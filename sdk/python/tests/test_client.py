"""Tests the Python client against a real sandboxd (the in-memory backend).

    SANDBOXD=bin/sandboxd python3 -m unittest discover -s sdk/python/tests

Starts its own sandboxd on a temporary unix socket. Skipped when SANDBOXD is
not set; `make test-sdk` sets it.
"""

import json
import os
import subprocess
import sys
import tempfile
import time
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from sandboxapi import ApiError, Client  # noqa: E402

SANDBOXD = os.environ.get("SANDBOXD", "")


@unittest.skipUnless(SANDBOXD, "set SANDBOXD to a sandboxd binary")
class ClientTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dir = tempfile.mkdtemp(prefix="sdkpy", dir="/tmp")
        sock = os.path.join(cls.dir, "s.sock")
        tok = os.path.join(cls.dir, "tok")
        with open(tok, "w") as f:
            f.write("python-sdk-test-token")
        os.chmod(tok, 0o600)
        cls.proc = subprocess.Popen([SANDBOXD, "--backend", "fake", "--listen", "unix://" + sock, "--token-file", tok,
                                     "--state-dir", os.path.join(cls.dir, "state")],
                                    stderr=subprocess.DEVNULL)
        for _ in range(100):
            if os.path.exists(sock):
                break
            time.sleep(0.02)
        cls.c = Client("unix://" + sock, token="python-sdk-test-token")

    @classmethod
    def tearDownClass(cls):
        cls.proc.terminate()
        cls.proc.wait(5)

    def new(self, **kw):
        sb = self.c.create_sandbox(**kw)
        self.addCleanup(lambda: self.c.terminate_sandbox(sb["id"]))
        return sb

    def test_capabilities(self):
        caps = self.c.capabilities()
        self.assertEqual(caps["api_version"], "v1")
        self.assertEqual(caps["backend"], "fake")

    def test_labels_and_events(self):
        sb = self.new(labels={"sdk": "python"}, env={"SDK_SECRET": "py-secret-value"})
        self.assertEqual(sb["labels"], {"sdk": "python"})
        self.assertEqual([s["id"] for s in self.c.sandboxes(labels={"sdk": "python"})], [sb["id"]])
        self.c.run(sb["id"], ["true"])
        events = self.c.events(sb["id"])["events"]
        self.assertEqual(events[0]["type"], "sandbox.created")
        self.assertIn("SDK_SECRET", events[0]["env_names"])
        self.assertNotIn("py-secret-value", json.dumps(events))
        self.assertIn("process.started", [e["type"] for e in events])

    def test_run_and_files(self):
        sb = self.new(env={"GREETING": "hello"})
        self.assertNotIn("hello", str(sb))  # values never come back
        res = self.c.run(sb["id"], ["printenv", "GREETING"])
        self.assertEqual(res["exit_code"], 0)
        self.assertEqual(res["stdout"], b"hello\n")
        self.assertEqual(self.c.run(sb["id"], ["cat"], stdin=b"in\x00out")["stdout"], b"in\x00out")
        self.c.write_file(sb["id"], "/workspace/a/b.bin", b"\x00\xffbytes")
        self.assertEqual(self.c.read_file(sb["id"], "/workspace/a/b.bin"), b"\x00\xffbytes")
        self.assertEqual([e["name"] for e in self.c.list_dir(sb["id"], "/workspace/a")], ["b.bin"])
        self.c.remove_file(sb["id"], "/workspace/a/b.bin")
        with self.assertRaises(ApiError) as e:
            self.c.read_file(sb["id"], "/workspace/a/b.bin")
        self.assertEqual(e.exception.code, "not_found")

    def test_background_process(self):
        sb = self.new()
        p = self.c.start_process(sb["id"], ["cat"])
        self.c.write_stdin(sb["id"], p["pid"], b"one\n")
        self.c.write_stdin(sb["id"], p["pid"], b"two\n", close=True)
        events = list(self.c.follow_output(sb["id"], p["pid"]))
        self.assertEqual(b"".join(e.get("data", b"") for e in events), b"one\ntwo\n")
        self.assertEqual(events[-1]["exit_code"], 0)

    def test_refusals_are_typed(self):
        with self.assertRaises(ApiError) as e:
            self.c.create_sandbox(network={"mode": "open"})
        self.assertEqual(e.exception.code, "refused")
        with self.assertRaises(ApiError) as e:
            self.c.create_sandbox(env={"LD_PRELOAD": "/x"})
        self.assertEqual(e.exception.code, "refused")
        with self.assertRaises(ApiError) as e:
            self.c.create_sandbox(network={"mode": "allowlist", "allow": []})
        self.assertEqual(e.exception.code, "refused")

    def test_omitted_allow_is_the_default_list(self):
        sb = self.new(network={"mode": "allowlist", "deny": ["gist.github.com"]})
        self.assertTrue(sb["network"]["allow"])
        self.assertEqual(sb["network"]["deny"], ["gist.github.com"])

    def test_wrong_token(self):
        bad = Client("unix://" + os.path.join(self.dir, "s.sock"), token="wrong")
        with self.assertRaises(ApiError) as e:
            bad.capabilities()
        self.assertEqual(e.exception.code, "unauthorized")


if __name__ == "__main__":
    unittest.main()
