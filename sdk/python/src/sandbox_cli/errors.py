"""What can go wrong, as types a caller can branch on.

The names are **not** a literal mirror of the TypeScript client's, and that is
deliberate: `TimeoutError` and `ConnectionError` are Python builtins, and
shadowing them in a library that agent code writes `except` around is how a
caller's `except ConnectionError` silently stops matching socket errors. So the
two that collide get names of their own, and the two that do not carry over
unchanged.
"""

from __future__ import annotations

from typing import Any


class SandboxError(Exception):
    """Base for everything this package raises deliberately."""


class ApiError(SandboxError):
    """The daemon answered, and said no.

    The daemon's own sentence is kept whole. It knows things this client does not
    — which container holds a name, which profile refused — and rewording it
    would lose the half that is actionable.
    """

    def __init__(self, status: int, endpoint: str, message: str) -> None:
        super().__init__(message)
        self.status = status
        self.endpoint = endpoint


class DaemonUnreachable(SandboxError):
    """No daemon answered at all — nothing was started.

    Named rather than `ConnectionError` because that is a builtin, and this is a
    different claim: not "a socket failed" but "there is no Studio here". The
    message says where it looked, since the commonest cause is that `studio.sh`
    is not running.
    """


class RunTimeout(SandboxError):
    """The wait gave up. Carries the run, because it still exists.

    Named rather than `TimeoutError` (a builtin, and an alias of OSError) for the
    same reason as above. The run is attached for the reason `WaitError` attaches
    it: a detached container holds `sandbox-<repo>-<branch>`, which docker will
    not duplicate, so an error without the id leaves the branch blocked by
    something the caller cannot name.
    """

    def __init__(self, run: dict[str, Any], message: str) -> None:
        super().__init__(message)
        self.run = run


class WaitError(SandboxError):
    """The launch succeeded and the wait did not.

    The container exists whatever went wrong here — a daemon restart mid-poll, a
    502 — so the run travels with the error.
    """

    def __init__(self, run: dict[str, Any], cause: BaseException) -> None:
        super().__init__(f"the run started but waiting for it failed: {cause}")
        self.run = run
        self.__cause__ = cause


class RunCancelled(SandboxError):
    """An async wait was cancelled. The run was stopped before this was raised.

    `asyncio.CancelledError` mid-wait would otherwise leave a container running
    and holding its branch's name — an `await` that is cancelled and leaves an
    agent working is the worst version of this API.
    """

    def __init__(self, run: dict[str, Any]) -> None:
        super().__init__(f"the wait was cancelled; run {run.get('id')} was stopped")
        self.run = run
