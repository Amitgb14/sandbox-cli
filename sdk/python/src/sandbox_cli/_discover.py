"""Where the daemon is, and what it will accept — without asking anybody to
paste anything.

`studio.sh` already writes both into ~/.config/sandbox/studio: a generated token,
and a `ports` file whose second line is the API port. A client that made the user
copy those out of a terminal would be asking for a credential the machine already
holds, which is how tokens end up in shell history and committed config.

Explicit arguments win, then the environment, then the files. The last of those
is the only one that can be wrong about *which* daemon — a laptop with two
checkouts has one studio state directory — so it is last.

Deliberately identical to the TypeScript client's discover.ts, including the
order and the fallback port. Two clients that find the daemon differently are two
support questions.
"""

from __future__ import annotations

import os
from pathlib import Path

DEFAULT_PORT = 8787


def _state_dir() -> Path:
    xdg = os.environ.get("XDG_CONFIG_HOME")
    base = Path(xdg) if xdg else Path.home() / ".config"
    return base / "sandbox" / "studio"


def _read_trimmed(path: Path) -> str | None:
    try:
        text = path.read_text(encoding="utf-8").strip()
    except OSError:
        return None
    return text or None


def discovered_port() -> int | None:
    """The API port studio.sh recorded, or None when it has never run here."""
    raw = _read_trimmed(_state_dir() / "ports")
    if not raw:
        return None
    # Two lines: the UI's port, then the API's. Read by position because that is
    # how the file is written; a malformed one answers None rather than a guess.
    lines = raw.split("\n")
    if len(lines) < 2:
        return None
    try:
        port = int(lines[1].strip())
    except ValueError:
        return None
    return port if port > 0 else None


def discover_url(explicit: str | None = None) -> str:
    if explicit:
        return explicit.rstrip("/")
    from_env = os.environ.get("SANDBOX_API_URL")
    if from_env:
        return from_env.rstrip("/")
    return f"http://127.0.0.1:{discovered_port() or DEFAULT_PORT}"


def discover_token(explicit: str | None = None) -> str:
    if explicit is not None:
        return explicit
    # Truthiness, not `is not None`, because the TypeScript client tests
    # truthiness and this module claims to be identical to it. The difference is
    # not academic: `SANDBOX_STUDIO_TOKEN=""` is what an unset passthrough in a
    # shell wrapper produces, and treating that as "the token is empty" skips the
    # file and sends no Authorization header — every request 401s where the other
    # client works.
    from_env = os.environ.get("SANDBOX_STUDIO_TOKEN")
    if from_env:
        return from_env
    return _read_trimmed(_state_dir() / "token") or ""
