"""The client. Byte fields (stdout, stderr, file contents) are ``bytes``; the
JSON wire format carries them base64-encoded, and this module does the
conversion so callers never see it."""

import base64
import http.client
import json
import socket
import ssl
import urllib.parse
from typing import Any, Dict, Iterator, List, Optional


class ApiError(Exception):
    """A non-2xx response. ``code`` is the API's error code (``refused``,
    ``unsupported``, ``not_found``, ...); see docs/api/v1.md."""

    def __init__(self, status: int, code: str, message: str):
        super().__init__(f"{code} ({status}): {message}")
        self.status = status
        self.code = code
        self.message = message


class _UnixConnection(http.client.HTTPConnection):
    def __init__(self, path: str, timeout: float):
        super().__init__("localhost", timeout=timeout)
        self._path = path

    def connect(self) -> None:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(self.timeout)
        s.connect(self._path)
        self.sock = s


def _b64(data: Optional[bytes]) -> Optional[str]:
    return None if data is None else base64.b64encode(data).decode()


def _unb64(s: Optional[str]) -> bytes:
    return base64.b64decode(s) if s else b""


class Client:
    """A client for one endpoint.

    ``endpoint`` is ``unix:///path/to/sandboxd.sock``, ``http://127.0.0.1:7070``
    or ``https://host:port``. ``ca_file`` trusts a private CA (the usual
    self-hosted case) instead of the system's.
    """

    def __init__(self, endpoint: str, token: str = "", ca_file: Optional[str] = None, timeout: float = 300.0):
        self._token = token
        self._timeout = timeout
        if endpoint.startswith("unix://"):
            self._unix = endpoint[len("unix://"):]
            if not self._unix:
                raise ValueError("endpoint names no socket")
            self._host = None
            return
        u = urllib.parse.urlparse(endpoint)
        if u.scheme not in ("http", "https") or not u.hostname:
            raise ValueError("endpoint: want http(s)://host[:port] or unix:///path")
        self._unix = None
        self._scheme = u.scheme
        self._host = u.hostname
        self._port = u.port
        self._ssl = None
        if u.scheme == "https":
            self._ssl = ssl.create_default_context(cafile=ca_file) if ca_file else ssl.create_default_context()

    # --- plumbing -------------------------------------------------------------

    def _conn(self) -> http.client.HTTPConnection:
        if self._unix is not None:
            return _UnixConnection(self._unix, self._timeout)
        if self._scheme == "https":
            return http.client.HTTPSConnection(self._host, self._port, timeout=self._timeout, context=self._ssl)
        return http.client.HTTPConnection(self._host, self._port, timeout=self._timeout)

    def _request(self, method: str, path: str, query: Optional[Dict[str, str]] = None,
                 body: Optional[bytes] = None, content_type: str = "") -> http.client.HTTPResponse:
        if query:
            path += "?" + urllib.parse.urlencode(query)
        headers = {}
        if content_type:
            headers["Content-Type"] = content_type
        if self._token:
            headers["Authorization"] = "Bearer " + self._token
        conn = self._conn()
        conn.request(method, path, body=body, headers=headers)
        resp = conn.getresponse()
        resp._sbx_conn = conn  # closed by _read, or by the caller of a stream
        if resp.status >= 300:
            raw = resp.read(64 << 10)
            conn.close()
            try:
                err = json.loads(raw)["error"]
                raise ApiError(resp.status, err.get("code", "internal"), err.get("message", ""))
            except (ValueError, KeyError, TypeError):
                raise ApiError(resp.status, "internal", raw.decode(errors="replace").strip())
        return resp

    @staticmethod
    def _read(resp: http.client.HTTPResponse) -> bytes:
        try:
            return resp.read()
        finally:
            resp._sbx_conn.close()

    def _json(self, method: str, path: str, payload: Any = None) -> Any:
        body = None if payload is None else json.dumps(payload).encode()
        resp = self._request(method, path, body=body, content_type="application/json" if body else "")
        data = self._read(resp)
        return json.loads(data) if data else None

    @staticmethod
    def _sbx(ref: str) -> str:
        return "/v1/sandboxes/" + urllib.parse.quote(ref, safe="")

    # --- capabilities and sandboxes -------------------------------------------

    def capabilities(self) -> Dict[str, Any]:
        return self._json("GET", "/v1/capabilities")

    def create_sandbox(self, *, name: str = "", image: str = "", cpus: float = 0, memory_mb: int = 0,
                       disk_mb: int = 0, env: Optional[Dict[str, str]] = None,
                       network: Optional[Dict[str, Any]] = None, idle_timeout_secs: int = 0,
                       snapshot_id: str = "",
                       labels: Optional[Dict[str, str]] = None,
                       volumes: Optional[List[Dict[str, Any]]] = None) -> Dict[str, Any]:
        """Create a sandbox. ``network`` is ``{"mode": "none"|"allowlist"|"open",
        "allow": [...], "deny": [...]}``; ``"allow": None`` (or absent) means the
        server's default list, ``[]`` means nothing — which is refused.
        ``labels`` are your own metadata: returned with the sandbox, usable to
        filter ``sandboxes()``, and recorded in its audit events. ``volumes`` is
        ``[{"name": ..., "path": ..., "read_only": bool}]`` (capability
        ``volumes``)."""
        req: Dict[str, Any] = {}
        for k, v in (("name", name), ("image", image), ("cpus", cpus), ("memory_mb", memory_mb),
                     ("disk_mb", disk_mb), ("env", env), ("network", network),
                     ("idle_timeout_secs", idle_timeout_secs), ("snapshot_id", snapshot_id),
                     ("labels", labels), ("volumes", volumes)):
            if v:
                req[k] = v
        if network is not None and "allow" not in network:
            req["network"] = dict(network, allow=None)
        return self._json("POST", "/v1/sandboxes", req)

    def sandbox(self, ref: str) -> Dict[str, Any]:
        return self._json("GET", self._sbx(ref))

    def sandboxes(self, labels: Optional[Dict[str, str]] = None) -> List[Dict[str, Any]]:
        """Newest first; with ``labels``, only sandboxes carrying every one."""
        path = "/v1/sandboxes"
        if labels:
            path += "?" + urllib.parse.urlencode([("label", f"{k}={v}") for k, v in labels.items()])
        return self._json("GET", path)["sandboxes"]

    def create_volume(self, name: str, size_mb: int = 0) -> Dict[str, Any]:
        """A named filesystem that outlives the sandboxes it is mounted in."""
        req: Dict[str, Any] = {"name": name}
        if size_mb:
            req["size_mb"] = size_mb
        return self._json("POST", "/v1/volumes", req)

    def volumes(self) -> List[Dict[str, Any]]:
        return self._json("GET", "/v1/volumes")["volumes"]

    def delete_volume(self, name: str) -> None:
        """Deletes the volume and everything on it; refused while it is mounted."""
        self._json("DELETE", "/v1/volumes/" + urllib.parse.quote(name, safe=""))

    def events(self, ref: str) -> Dict[str, Any]:
        """The sandbox's audit events, oldest first: ``{"events": [...],
        "truncated": bool}`` (capability ``audit``). Environment variables
        appear by name only."""
        return self._json("GET", self._sbx(ref) + "/events")

    def update_network(self, ref: str, network: Dict[str, Any]) -> Dict[str, Any]:
        return self._json("PATCH", self._sbx(ref), {"network": network})

    def terminate_sandbox(self, ref: str) -> None:
        self._json("DELETE", self._sbx(ref))

    def suspend(self, ref: str) -> Dict[str, Any]:
        return self._json("POST", self._sbx(ref) + "/suspend")

    def resume(self, ref: str) -> Dict[str, Any]:
        return self._json("POST", self._sbx(ref) + "/resume")

    def create_snapshot(self, ref: str) -> Dict[str, Any]:
        return self._json("POST", self._sbx(ref) + "/snapshots")

    def snapshots(self) -> List[Dict[str, Any]]:
        return self._json("GET", "/v1/snapshots")["snapshots"]

    def delete_snapshot(self, snapshot_id: str) -> None:
        self._json("DELETE", "/v1/snapshots/" + urllib.parse.quote(snapshot_id, safe=""))

    # --- processes -------------------------------------------------------------

    def run(self, ref: str, argv: List[str], *, env: Optional[Dict[str, str]] = None, cwd: str = "",
            stdin: Optional[bytes] = None, timeout_secs: int = 0) -> Dict[str, Any]:
        """Run to completion. Returns ``exit_code``, ``stdout`` and ``stderr``
        (bytes), ``truncated`` and ``timed_out``."""
        req: Dict[str, Any] = {"argv": argv}
        if env:
            req["env"] = env
        if cwd:
            req["cwd"] = cwd
        if stdin:
            req["stdin"] = _b64(stdin)
        if timeout_secs:
            req["timeout_secs"] = timeout_secs
        out = self._json("POST", self._sbx(ref) + "/run", req)
        out["stdout"] = _unb64(out.get("stdout"))
        out["stderr"] = _unb64(out.get("stderr"))
        return out

    def start_process(self, ref: str, argv: List[str], *, env: Optional[Dict[str, str]] = None,
                      cwd: str = "") -> Dict[str, Any]:
        req: Dict[str, Any] = {"argv": argv}
        if env:
            req["env"] = env
        if cwd:
            req["cwd"] = cwd
        return self._json("POST", self._sbx(ref) + "/processes", req)

    def processes(self, ref: str) -> List[Dict[str, Any]]:
        return self._json("GET", self._sbx(ref) + "/processes")["processes"]

    def process(self, ref: str, pid: int) -> Dict[str, Any]:
        return self._json("GET", f"{self._sbx(ref)}/processes/{pid}")

    def follow_output(self, ref: str, pid: int) -> Iterator[Dict[str, Any]]:
        """Yield output events from the start: ``{"stream": "stdout"|"stderr",
        "data": bytes}``, and last ``{"exit_code": n}``."""
        resp = self._request("GET", f"{self._sbx(ref)}/processes/{pid}/output")
        try:
            for line in resp:
                ev = json.loads(line)
                if "data" in ev:
                    ev["data"] = _unb64(ev["data"])
                yield ev
        finally:
            resp.close()
            resp._sbx_conn.close()

    def write_stdin(self, ref: str, pid: int, data: bytes, close: bool = False) -> None:
        self._read(self._request("POST", f"{self._sbx(ref)}/processes/{pid}/stdin", {"close": "1"} if close else None,
                                 body=data, content_type="application/octet-stream"))

    def signal(self, ref: str, pid: int, signal: str) -> None:
        self._json("POST", f"{self._sbx(ref)}/processes/{pid}/signal", {"signal": signal})

    # --- files -----------------------------------------------------------------

    def read_file(self, ref: str, path: str) -> bytes:
        return self._read(self._request("GET", self._sbx(ref) + "/files", {"path": path}))

    def write_file(self, ref: str, path: str, data: bytes) -> None:
        self._read(self._request("PUT", self._sbx(ref) + "/files", {"path": path}, body=data,
                      content_type="application/octet-stream"))

    def remove_file(self, ref: str, path: str) -> None:
        self._read(self._request("DELETE", self._sbx(ref) + "/files", {"path": path}))

    def list_dir(self, ref: str, path: str) -> List[Dict[str, Any]]:
        return json.loads(self._read(self._request("GET", self._sbx(ref) + "/dirs", {"path": path})))["entries"]

    # --- gateway ---------------------------------------------------------------
    # A gateway in front of many sandboxd nodes adds these; a plain sandboxd
    # answers each with ApiError code ``not_found``.

    def whoami(self) -> Dict[str, Any]:
        """The caller as the gateway sees its API key: ``user``, ``tenant``,
        ``key_id`` and ``scopes``."""
        return self._json("GET", "/v1/whoami")

    def ssh_info(self) -> Dict[str, Any]:
        """Where the gateway's SSH server listens (``host``, ``port``) and the
        host keys to pin (``host_keys``, ``fingerprint``)."""
        return self._json("GET", "/v1/ssh")

    def add_ssh_key(self, key: str, sandbox: str = "") -> Dict[str, Any]:
        """Register a public key (one authorized_keys line, no options) for SSH
        logins: ``ssh SANDBOX@host -p port``. ``sandbox`` limits it to one."""
        req: Dict[str, Any] = {"key": key}
        if sandbox:
            req["sandbox"] = sandbox
        return self._json("POST", "/v1/ssh-keys", req)

    def ssh_keys(self) -> List[Dict[str, Any]]:
        return self._json("GET", "/v1/ssh-keys")["keys"]

    def remove_ssh_key(self, key_id: str) -> None:
        self._json("DELETE", "/v1/ssh-keys/" + urllib.parse.quote(key_id, safe=""))

    def ssh_access(self, ref: str, ttl_secs: int = 0) -> Dict[str, Any]:
        """A short-lived SSH login to one sandbox: ``user`` (the token, and the
        whole credential until ``expires_at``), ``host``, ``port`` and the
        ``command`` that uses it. ``ttl_secs`` 0 takes the gateway's default."""
        req: Dict[str, Any] = {}
        if ttl_secs:
            req["ttl_secs"] = ttl_secs
        return self._json("POST", self._sbx(ref) + "/ssh-access", req)

    # --- jobs, agent runs and secrets (gateway) --------------------------------
    # Work the gateway runs after the request that started it has gone: a
    # sandbox per run, made, watched and taken down by the gateway.

    def create_job(self, spec: Dict[str, Any]) -> Dict[str, Any]:
        """Start a job. ``spec`` holds the API's keys: ``command`` (an argv) or
        ``agent`` and ``prompt`` (or ``prompts``, one run each), and optionally
        ``name``, ``image``, ``parallelism``, ``completions``, ``retries``,
        ``timeout_secs``, ``env``, ``secrets`` (names), ``network``,
        ``resources``, ``from_snapshot``, ``keep`` (``{"output": bool,
        "files": [paths]}``) and ``notify`` (a URL)."""
        return self._json("POST", "/v1/jobs", spec)

    def agent_run(self, agent: str, prompt: str, **options: Any) -> Dict[str, Any]:
        """Start a one-run job of ``agent`` on ``prompt``. ``options`` are the
        job keys that apply to one run: ``secrets``, ``env``, ``image``,
        ``timeout_secs``, ``retries``, ``keep``, ``notify``, ..."""
        req: Dict[str, Any] = {"agent": agent, "prompt": prompt}
        req.update(options)
        return self._json("POST", "/v1/agent-runs", req)

    def jobs(self) -> List[Dict[str, Any]]:
        """The caller's jobs, newest first, without their runs."""
        return self._json("GET", "/v1/jobs")["jobs"]

    @staticmethod
    def _job(job_id: str) -> str:
        return "/v1/jobs/" + urllib.parse.quote(job_id, safe="")

    def job(self, job_id: str) -> Dict[str, Any]:
        """One job, with each run's state, exit code, attempts and sandbox."""
        return self._json("GET", self._job(job_id))

    def cancel_job(self, job_id: str) -> Dict[str, Any]:
        """Cancel a job: runs not started never are; running ones' sandboxes
        are terminated. Returns the job."""
        return self._json("DELETE", self._job(job_id))

    def job_output(self, job_id: str, run: int) -> Dict[str, Any]:
        """What run ``run`` kept of its output: ``stdout`` and ``stderr`` as
        bytes, and ``truncated``."""
        out = self._json("GET", "%s/runs/%d/output" % (self._job(job_id), run))
        return {"stdout": _unb64(out.get("stdout")), "stderr": _unb64(out.get("stderr")),
                "truncated": out.get("truncated", False)}

    def job_file(self, job_id: str, run: int, path: str) -> bytes:
        """A file run ``run`` kept (one of the job's ``keep.files``)."""
        return self._read(self._request("GET", "%s/runs/%d/files" % (self._job(job_id), run), {"path": path}))

    def set_secret(self, name: str, value: str) -> None:
        """Set one of the tenant's secrets. The value is never returned."""
        self._json("PUT", "/v1/secrets/" + urllib.parse.quote(name, safe=""), {"value": value})

    def secrets(self) -> List[Dict[str, Any]]:
        """The tenant's secrets: ``name`` and ``updated_at``, never a value."""
        return self._json("GET", "/v1/secrets")["secrets"]

    def delete_secret(self, name: str) -> None:
        self._json("DELETE", "/v1/secrets/" + urllib.parse.quote(name, safe=""))
