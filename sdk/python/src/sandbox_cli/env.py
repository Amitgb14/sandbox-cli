"""Reading environment configuration from a file.

A `.env` loader is a small thing that moves secrets, so it is explicit here
rather than automatic: this returns a dict and the caller passes it to `env=`.
Nothing is read unless somebody names a file, and nothing reaches a container
unless somebody passes it to a run.

Two things worth knowing before you point this at a real `.env`. Values travel in
the **request body** to the daemon, so on a remote daemon without TLS they cross
the network in cleartext — the same caveat the Studio docs give for the token
itself. And a value handed to a run is readable by the agent or code inside that
container, which is the point of forwarding it and also the reason to forward one
key rather than a file of them.
"""

from __future__ import annotations

import os
from pathlib import Path


def read_env_file(path: str | os.PathLike[str], *, missing_ok: bool = False) -> dict[str, str]:
    """Parse a `.env`-style file into a dict.

    Deliberately a small parser rather than a dependency, and deliberately a
    strict one: `KEY=value` lines, `#` comments, blank lines, optional `export `,
    and quotes stripped when they wrap the whole value. Anything else raises with
    the line number instead of being skipped — a silently ignored line in a
    credentials file is how a run goes out without the key it needed.

    No interpolation of `$OTHER`, and no shell semantics. This file is data.
    """
    p = Path(path)
    if not p.exists():
        if missing_ok:
            return {}
        raise FileNotFoundError(f"no env file at {p}")

    out: dict[str, str] = {}
    for number, raw in enumerate(p.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export "):].lstrip()
        key, sep, value = line.partition("=")
        key = key.strip()
        if not sep or not key:
            raise ValueError(f"{p}:{number}: expected KEY=value, got {raw!r}")
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        out[key] = value
    return out
