"""The async face.

One implementation, not two. Every method here is the sync one run in a thread,
because two hand-written clients of one protocol drift — which is the failure
`internal/contract` exists to prevent across languages, and it would be strange
to solve it there and reintroduce it inside one.

The stdlib has no async HTTP client (`urllib` and `http.client` block; `asyncio`
gives sockets, not HTTP), so a *native* async client would mean a dependency
while the sync one needs none. A code-execution SDK that drags an HTTP stack into
somebody's agent process is a worse neighbour than one that does not.

What it costs, said plainly: a thread per in-flight call. For this workload that
is the right trade — a run spends its life *waiting* on a container, and an agent
orchestrating tens of sandboxes is nowhere near where threads hurt. If
single-threaded async at scale is ever wanted, an httpx-backed transport goes
behind the same interface and this API does not move.
"""

from __future__ import annotations

import asyncio
import threading
from typing import Any

from ._client import Outcome, Project, Studio, Workspace
from .errors import RunCancelled


async def _off_thread(fn, /, *args, **kwargs):
    return await asyncio.to_thread(fn, *args, **kwargs)


class AsyncStudio:
    def __init__(self, sync: Studio) -> None:
        self._sync = sync

    @classmethod
    async def connect(cls, url: str | None = None, token: str | None = None) -> "AsyncStudio":
        return cls(await _off_thread(Studio.connect, url, token))

    @property
    def url(self) -> str:
        return self._sync.url

    async def health(self) -> dict[str, Any]:
        return await _off_thread(self._sync.health)

    async def projects(self) -> list["AsyncProject"]:
        return [AsyncProject(p) for p in await _off_thread(self._sync.projects)]

    async def project(self, id_or_name: str | None = None) -> "AsyncProject":
        return AsyncProject(await _off_thread(self._sync.project, id_or_name))

    async def add_project(self, path: str | None = None, *, init: bool = False) -> "AsyncProject":
        return AsyncProject(await _off_thread(self._sync.add_project, path, init=init))

    async def clone(self, url: str, parent: str, name: str | None = None) -> "AsyncProject":
        return AsyncProject(await _off_thread(self._sync.clone, url, parent, name))

    async def runs(self, *, all: bool = False, repo: str | None = None) -> list[dict[str, Any]]:
        return await _off_thread(self._sync.runs, all=all, repo=repo)


class AsyncProject:
    def __init__(self, sync: Project) -> None:
        self._sync = sync
        self.id = sync.id
        self.name = sync.name
        self.root = sync.root
        self.missing = sync.missing

    def __repr__(self) -> str:
        return f"AsyncProject(id={self.id!r}, name={self.name!r})"

    async def workspace(self, branch: str, *, env: dict[str, str] | None = None) -> "AsyncWorkspace":
        return AsyncWorkspace(await _off_thread(self._sync.workspace, branch, env=env))


class AsyncWorkspace:
    def __init__(self, sync: Workspace) -> None:
        self._sync = sync
        self.project = sync.project
        self.branch = sync.branch
        self.env = sync.env

    def __repr__(self) -> str:
        return f"AsyncWorkspace(project={self.project.name!r}, branch={self.branch!r})"

    async def run(self, argv: list[str], **opts: Any) -> Outcome:
        return await self._guarded(self._sync.run, argv, **opts)

    async def agent(self, name: str, prompt: str, **opts: Any) -> Outcome:
        return await self._guarded(self._sync.agent, name, prompt, **opts)

    async def steps(self, commands: list[list[str]], **opts: Any) -> list[Outcome]:
        """Run commands in order, stopping at the first failure. See the sync
        docstring — the stopping rule is the whole point."""
        done: list[Outcome] = []
        for argv in commands:
            out = await self.run(argv, **opts)
            done.append(out)
            if out.exit_code != 0 or out.stopped:
                break
        return done

    async def start(self, argv: list[str], **opts: Any) -> dict[str, Any]:
        """Launch without waiting. See the sync docstring.

        Guarded like `run` and `agent`, and for the same reason rather than for
        symmetry: `asyncio.to_thread` cannot be interrupted, so a cancelled await
        still completes its POST in the abandoned thread. Without the guard that
        container exists, holds its branch's name, and no caller ever saw its id.
        The launch budget is ten minutes — a first run builds the base image — so
        the window is real rather than theoretical.
        """
        task = asyncio.create_task(_off_thread(self._sync.start, argv, **opts))
        try:
            return await asyncio.shield(task)
        except asyncio.CancelledError:
            run_id = self._sync._in_flight  # noqa: SLF001 — same package
            if run_id is not None:
                try:
                    await _off_thread(self._sync.stop, run_id, False)
                finally:
                    task.cancel()
                raise RunCancelled({"id": run_id}) from None
            # Cancelled *during* the POST, which is the harder half: the request
            # is in a thread nothing can interrupt, so it will land and create a
            # container after this coroutine is gone. Arrange to stop it when it
            # does, rather than raise promptly and leave a container running that
            # nobody holds the id of.
            task.add_done_callback(self._stop_when_it_lands)
            raise

    def _stop_when_it_lands(self, task: "asyncio.Task[dict[str, Any]]") -> None:
        """Stop a run whose launch outlived the caller who asked for it.

        In a plain thread rather than on the loop: this fires from a done
        callback, the loop may be closing, and the stop is a courtesy that must
        not depend on anything still running.
        """
        if task.cancelled() or task.exception() is not None:
            return
        started = task.result()
        run_id = started.get("id")
        if not run_id:
            return
        threading.Thread(target=self._sync.stop, args=(run_id, False), daemon=True).start()

    async def clear_finished(self) -> list[str]:
        return await _off_thread(self._sync.clear_finished)

    async def stop(self, run_id: str, force: bool = False) -> None:
        await _off_thread(self._sync.stop, run_id, force)

    async def remove(self, run_id: str) -> None:
        await _off_thread(self._sync.remove, run_id)

    async def logs(self, run_id: str) -> list[dict[str, Any]]:
        return await _off_thread(self._sync.logs, run_id)

    async def _guarded(self, fn, /, *args, **kwargs) -> Outcome:
        """Run a launch-and-wait, and never leave a container behind on cancel.

        `asyncio.CancelledError` mid-wait is the same situation as WaitError: the
        launch already succeeded, so the container exists and holds its branch's
        name — nothing else can take it, including the next run of this script.

        Two things this has to get right, and the first version got both wrong.
        It stops **the run this call launched**, read from the workspace rather
        than searched for by branch: a search finds whatever live run carries the
        label, which on a busy branch is another process's agent, and killing
        that is the exact opposite of the promise above.

        And it tells the worker to stop polling. `asyncio.to_thread` cannot be
        interrupted, so a cancelled await used to leave a thread polling until
        the run deadline — up to thirty minutes — with `asyncio.run` blocking on
        executor shutdown behind it. The abort flag turns that into one poll
        interval.
        """
        task = asyncio.create_task(_off_thread(fn, *args, **kwargs))
        try:
            return await asyncio.shield(task)
        except asyncio.CancelledError:
            run_id = self._sync._in_flight  # noqa: SLF001 — same package
            self._sync._abort.set()  # noqa: SLF001
            if run_id is None:
                # Cancelled before anything was launched, or after it finished:
                # there is nothing out there to stop.
                raise
            try:
                await _off_thread(self._sync.stop, run_id, False)
            finally:
                task.cancel()
            raise RunCancelled({"id": run_id}) from None
