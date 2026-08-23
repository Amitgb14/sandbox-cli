"""One HTTP conversation with the daemon, on the standard library.

No dependency, on purpose: this package is imported into somebody's agent
process, and an HTTP stack is a bad thing to drag in behind them. `urllib` is
enough for JSON over loopback, and the async face runs these calls in a thread
rather than duplicating them against a second stack.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.request
from typing import Any

from ._discover import discover_token, discover_url
from .errors import ApiError, DaemonUnreachable

DEFAULT_TIMEOUT_S = 30.0


class Transport:
    def __init__(self, url: str | None = None, token: str | None = None,
                 timeout: float = DEFAULT_TIMEOUT_S) -> None:
        self.url = discover_url(url)
        self.token = discover_token(token)
        self.timeout = timeout

    def request(self, method: str, path: str, body: Any | None = None,
                timeout: float | None = None) -> Any:
        data = json.dumps(body).encode() if body is not None else None
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        req = urllib.request.Request(self.url + path, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=timeout or self.timeout) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            # The daemon's sentence, whole. It knows which container holds a name
            # and which profile refused; a summary here would drop the half that
            # tells somebody what to do.
            detail = _error_text(e.read())
            raise ApiError(e.code, f"{method} {path}", detail) from None
        except urllib.error.URLError as e:
            raise DaemonUnreachable(
                f"no Studio daemon answered at {self.url} ({e.reason}). "
                f"Start one with `studio.sh up`, or pass url= if it runs elsewhere."
            ) from None
        return json.loads(raw) if raw else None


def _error_text(raw: bytes) -> str:
    try:
        parsed = json.loads(raw)
    except ValueError:
        return raw.decode(errors="replace").strip() or "the daemon refused without saying why"
    if isinstance(parsed, dict) and isinstance(parsed.get("error"), str):
        return parsed["error"]
    return json.dumps(parsed)
