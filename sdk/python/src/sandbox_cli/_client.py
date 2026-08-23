"""The sync client. One implementation; `sandbox_cli.aio` is a face over it.

Everything here is a client of the Studio daemon and nothing more. No docker
socket, no shelling out to sandbox-cli, no argv assembled locally: every gate
that makes a sandbox a sandbox is applied where the container is built.
"""

from __future__ import annotations

import os
import re
import threading
import time
from dataclasses import dataclass, field
from typing import Any, Iterator

from ._local import init_repo, local_repo, same_path, unborn_with_files, wire_path
from ._transport import Transport
from .errors import ApiError, RequestTimeout, WaitError

DEFAULT_RUN_TIMEOUT_S = 30 * 60
_LAUNCH_TIMEOUT_S = 10 * 60
"""How long `POST /v1/runs` may take. Generous on purpose: the daemon builds the
base image lazily, so a first launch on a fresh machine is minutes rather than
seconds, and the 30-second default made that look like a daemon that was not
running."""
_POLL_MIN_S = 0.25
_POLL_MAX_S = 2.0

_FINISHED = ("exited", "dead")


@dataclass
class Outcome:
    """How a run ended, and who ended up doing the work."""

    id: str
    exit_code: int
    stdout: str
    stderr: str
    stopped: bool
    """True when the wait gave up and stopped the run rather than the run ending
    on its own terms. The exit code of a stopped container is not a verdict."""
    agent: str | None = None
    routed_from: str | None = None
    """Set when routing put a different agent on the work. Reported on every
    outcome rather than behind an option: a script that cannot see a failover
    credits the wrong agent — and bills the wrong account."""
    route_reason: str | None = None
    run: dict[str, Any] = field(default_factory=dict)


class Studio:
    """A daemon this process can ask for containers."""

    def __init__(self, transport: Transport) -> None:
        self._t = transport

    @classmethod
    def connect(cls, url: str | None = None, token: str | None = None) -> "Studio":
        """Point at a daemon. With no arguments it finds the one on this machine."""
        return cls(Transport(url, token))

    @property
    def url(self) -> str:
        return self._t.url

    def health(self) -> dict[str, Any]:
        return self._t.request("GET", "/v1/health")

    def projects(self) -> list["Project"]:
        res = self._t.request("GET", "/v1/projects") or {}
        return [Project(self._t, p) for p in (res.get("projects") or [])]

    def project(self, id_or_name: str | None = None) -> "Project":
        """One repository — by id, by name, by path, or with no argument at all,
        which asks git which repository the current directory belongs to.

        The no-argument form is a **lookup**: the root is matched against what the
        daemon already knows, so a directory nobody registered is refused and told
        which roots exist. Registering is `add_project`, deliberately — the
        registry is the list of directories that daemon will touch, and a lookup
        that quietly grew it would turn a typo into a permanent entry.
        """
        if id_or_name is None:
            return self._project_here()
        if id_or_name == "":
            raise ValueError(
                "project() was given an empty repository name — a missing argument rather "
                "than a request. Call project() with no arguments to mean the repository "
                "this script is in."
            )
        all_projects = self.projects()
        for p in all_projects:
            if p.id == id_or_name:
                return p
        named = [p for p in all_projects if p.name == id_or_name]
        if len(named) == 1:
            return named[0]
        if len(named) > 1:
            raise ValueError(
                f"{len(named)} repositories are called {id_or_name}; use an id: "
                + ", ".join(p.id for p in named)
            )
        if _looks_like_path(id_or_name):
            return self._project_at(id_or_name, all_projects)
        known = ", ".join(p.name for p in all_projects) or "none"
        raise ValueError(
            f"no repository {id_or_name} is registered with the daemon at {self._t.url}. "
            f"Registered: {known}. Add one with studio.add_project('/abs/path'), or in Studio."
        )

    def add_project(self, path: str | None = None, *, init: bool = False) -> "Project":
        """Register a directory on the **daemon's** machine as a repository.

        The one call here that hands over a path, mirroring the one endpoint that
        accepts one: the checks a directory has to pass — absolute, on disk, a git
        repository, not your home or an ancestor of it — are applied there, once,
        by the daemon.

        `init=True` runs `git init` first when the directory is not a repository
        yet. Opt-in, because creating a repository is a larger side effect than
        registering one, and because `git init` runs on *this* machine while the
        path belongs to the daemon's.
        """
        cwd = os.getcwd()
        if path is None:
            here = local_repo(cwd)
            if here is None and init:
                init_repo(cwd)
                here = local_repo(cwd)
            if here is None:
                raise ValueError(
                    f"{cwd} is not inside a git repository — Studio addresses work by branch, "
                    "so there has to be one. Pass init=True to run `git init` here first, or "
                    "give add_project a path."
                )
            asked = here.root
        else:
            typed = path.strip()
            if not typed:
                raise ValueError("add_project needs a path on the daemon's machine")
            asked = wire_path(typed, cwd)
            if init and local_repo(asked) is None:
                init_repo(asked)

        # A repository with files and no commits is registerable and useless:
        # every worktree Studio makes from it is empty, so the agent starts in a
        # /workspace with none of this code in it and nothing says so.
        if unborn_with_files(asked):
            raise ValueError(
                f"{asked} is a git repository with no commits yet, and Studio works from "
                "committed state — every worktree it makes would be empty, so an agent would "
                "see none of these files. Commit first (check what you are adding: a directory "
                "that was never a repository usually has no .gitignore), then add it:\n"
                '  git add -A && git commit -m "initial commit"'
            )
        return Project(self._t, self._t.request("POST", "/v1/projects", {"path": asked}))

    def clone(self, url: str, parent: str, name: str | None = None) -> "Project":
        """Clone a repository onto the **daemon's** machine and register it.

        `url` may be a full git URL or the GitHub shorthand `owner/repo`, which
        expands to `https://github.com/owner/repo.git`. The expansion is spelled
        out rather than clever: exactly one host is assumed, and anything with a
        scheme or a colon is left alone.

        `parent` is the directory to clone *into*, on that machine — the clone is
        the daemon writing to its own disk and running git, which is why its
        refusals are the substance of the endpoint: only https, ssh and
        `git@host:path` URLs, never `ext::` (which executes a command rather than
        fetching a repository), and the parent must pass the same checks a typed
        project path does.

        Private repositories are the daemon's business, not this client's: it
        uses whatever credentials that machine's git has. Putting a token in the
        URL would write it into the daemon's remote config and this SDK's logs.
        """
        body = {"url": _expand_repo_url(url), "parent": wire_path(parent, os.getcwd())}
        if name:
            body["name"] = name
        return Project(self._t, self._t.request("POST", "/v1/projects/clone", body))

    def runs(self, *, all: bool = False, repo: str | None = None) -> list[dict[str, Any]]:
        query = []
        if all:
            query.append("all=1")
        if repo:
            query.append(f"repo={repo}")
        suffix = "?" + "&".join(query) if query else ""
        return (self._t.request("GET", f"/v1/runs{suffix}") or {}).get("runs") or []

    # -- internals -------------------------------------------------------

    def _project_here(self) -> "Project":
        cwd = os.getcwd()
        here = local_repo(cwd)
        if here is None:
            raise ValueError(
                f"{cwd} is not inside a git repository, so there is nothing here to work on. "
                "Name one — studio.project('my-app') — or add a directory on the daemon's "
                "machine with studio.add_project('/abs/path')."
            )
        return self._match(here.root, here.tree, self.projects())

    def _project_at(self, path: str, all_projects: list["Project"]) -> "Project":
        here = local_repo(path)
        root = here.root if here else wire_path(path, os.getcwd())
        tree = here.tree if here else ""
        return self._match(root, tree, all_projects)

    def _match(self, root: str, tree: str, all_projects: list["Project"]) -> "Project":
        # Both forms are compared: a daemon started *inside* a linked worktree
        # registers its default project as that worktree, while every added
        # repository carries the main root.
        for p in all_projects:
            if same_path(p.root, root) or (tree and same_path(p.root, tree)):
                return p
        known = ", ".join(p.root for p in all_projects) or "none"
        raise ValueError(
            f"the daemon at {self._t.url} lists no repository at {root}. Either it has not "
            f"been added — studio.add_project({root!r}) — or that daemon is on another "
            f"machine, where this path means nothing. It knows: {known}."
        )


class Project:
    """A repository the daemon has been told about."""

    def __init__(self, transport: Transport, record: dict[str, Any]) -> None:
        self._t = transport
        self._record = record
        self.id: str = record.get("id", "")
        self.name: str = record.get("name", "")
        self.root: str = record.get("root", "")
        self.missing: bool = bool(record.get("missing"))

    def __repr__(self) -> str:
        return f"Project(id={self.id!r}, name={self.name!r})"

    def workspace(self, branch: str, *, env: dict[str, str] | None = None) -> "Workspace":
        """A branch's worktree, created if it does not exist.

        The isolation unit: one branch, one tree, many runs. Two agents working in
        one tree is a data race with a filesystem in the middle, which is why this
        is the only way to get somewhere to run.
        """
        self._t.request("POST", "/v1/worktrees", {"repo": self.id, "branch": branch})
        return Workspace(self._t, self, branch, env=env)


class Workspace:
    """A branch's worktree on a repository: where runs happen."""

    def __init__(self, transport: Transport, project: Project, branch: str,
                 *, env: dict[str, str] | None = None) -> None:
        self._t = transport
        self.project = project
        self.branch = branch
        self._in_flight: str | None = None
        """The run this workspace launched and is still waiting on.

        The async face needs it: `asyncio.to_thread` cannot be interrupted, so on
        cancellation it has to stop the run *by id*. Searching for a live run on
        this branch instead would find — and kill — another process's agent."""
        self._abort = threading.Event()
        """Set by the async face on cancellation, so the polling loop in the
        abandoned thread stops promptly instead of running to the deadline and
        holding up interpreter shutdown."""
        self.env: dict[str, str] = dict(env or {})
        """Environment applied to every run here, with a run's own `env=` winning
        per key. Configuration usually belongs to the *workspace* rather than to
        one command — `CI=true` is true of the whole sequence — and repeating it
        on each step is how one step ends up different from the others."""
        self._delivered: str | None = None
        """The run this object last returned an Outcome for — the one run that can
        be cleared without discarding anything, because its exit code, stdout and
        stderr were handed to the caller before it was recorded here."""

    def __repr__(self) -> str:
        return f"Workspace(project={self.project.name!r}, branch={self.branch!r})"

    def run(self, argv: list[str], **opts: Any) -> Outcome:
        if not argv:
            raise ValueError("a run needs a command")
        return self._launch_and_wait({"command": list(argv), **self._common(opts)}, opts)

    def agent(self, name: str, prompt: str, **opts: Any) -> Outcome:
        if not prompt.strip():
            raise ValueError("an agent run needs a prompt: it is the whole instruction")
        body: dict[str, Any] = {"agent": name, "prompt": prompt, **self._common(opts)}
        fallback = opts.get("fallback")
        if fallback:
            body["fallback"] = list(fallback)
        return self._launch_and_wait(body, opts)

    def steps(self, commands: list[list[str]], **opts: Any) -> list[Outcome]:
        """Run commands in order, stopping at the first one that fails.

        The shape almost every setup wants — install, build, test — and the
        reason it is here rather than left to a `for` loop is the stopping rule:
        a loop that runs all of them reports the *last* exit code, so a failed
        install followed by a passing lint looks like success.

        Returns what actually ran, so the caller can see where it stopped. Each
        step is a separate container, and only the worktree survives between
        them: `/tmp` does not, `/workspace` does.
        """
        done: list[Outcome] = []
        for argv in commands:
            out = self.run(argv, **opts)
            done.append(out)
            if out.exit_code != 0 or out.stopped:
                break
        return done

    def clear_finished(self) -> list[str]:
        """Remove finished runs holding this branch's container name.

        Refuses to touch one that is still going: "finished" is the whole of the
        claim, and a helper that killed a live agent to free a name would be the
        opposite of this tool.
        """
        removed = []
        for r in (self._t.request("GET", "/v1/runs?all=1") or {}).get("runs") or []:
            if r.get("branch") != self.branch or r.get("repoId") not in (None, self.project.id):
                continue
            if r.get("state") not in _FINISHED:
                continue
            self._t.request("DELETE", f"/v1/runs/{r['id']}")
            removed.append(r["id"])
        return removed

    def stop(self, run_id: str, force: bool = False) -> None:
        self._t.request("POST", f"/v1/runs/{run_id}/stop", {"force": force})

    def remove(self, run_id: str) -> None:
        """Remove the container. Deliberately separate from stop, and never called
        for you: a finished run's logs are the evidence for what it did."""
        self._t.request("DELETE", f"/v1/runs/{run_id}")

    def logs(self, run_id: str) -> list[dict[str, Any]]:
        """The run's log document: a bare array of lines, each with a stream.

        Not wrapped in an object — checked against the daemon rather than
        assumed, which is the difference between this working and a client that
        quietly reports empty output for every run.
        """
        lines = self._t.request("GET", f"/v1/runs/{run_id}/logs")
        return lines if isinstance(lines, list) else []

    # -- internals -------------------------------------------------------

    _RUN_OPTIONS = frozenset({"env", "allow", "memory", "cpus", "base", "publish",
                              "verify", "image", "timeout", "replace_finished", "fallback"})

    def _common(self, opts: dict[str, Any]) -> dict[str, Any]:
        # A typo here is silent otherwise, and one of these names is a security
        # control: `alow=["api.example.com"]` would launch with the daemon's
        # default egress posture and report success.
        unknown = sorted(set(opts) - self._RUN_OPTIONS)
        if unknown:
            raise TypeError(
                f"unknown run option(s): {', '.join(unknown)}. "
                f"Known: {', '.join(sorted(self._RUN_OPTIONS))}"
            )
        body: dict[str, Any] = {"repo": self.project.id, "worktree": self.branch,
                                "branch": self.branch}
        if self.env or opts.get("env"):
            # The workspace's environment is the base and the run's wins per key,
            # which is the merge people expect from "defaults".
            body["env"] = {**self.env, **(opts.get("env") or {})}
        for key, wire in (("allow", "allow"), ("memory", "memory"),
                          ("cpus", "cpus"), ("base", "base"), ("publish", "publish"),
                          ("verify", "verify"), ("image", "image")):
            if opts.get(key) is not None:
                body[wire] = opts[key]
        return body

    def _launch_and_wait(self, body: dict[str, Any], opts: dict[str, Any]) -> Outcome:
        try:
            started = self._t.request("POST", "/v1/runs", body, timeout=_LAUNCH_TIMEOUT_S)
        except ApiError as err:
            if err.status != 409:
                raise
            # A second step in the same script is not a second script. The name is
            # held by the run this object already returned an Outcome for, so
            # clearing it discards no evidence anybody is still waiting for — and
            # the alternative is that run() twice on one workspace cannot work.
            if self._delivered and not opts.get("replace_finished"):
                spent, self._delivered = self._delivered, None
                try:
                    try:
                        self.remove(spent)
                    except ApiError as gone:
                        # Already removed — by the caller, or by clear_finished.
                        # That is not the conflict the user needs to hear about.
                        if gone.status != 404:
                            raise
                    started = self._t.request("POST", "/v1/runs", body, timeout=_LAUNCH_TIMEOUT_S)
                    return self._await_outcome(started, opts)
                except ApiError as retry_err:
                    if retry_err.status != 409:
                        raise
                    err = retry_err
            if not opts.get("replace_finished"):
                raise ApiError(err.status, err.endpoint, str(err) +
                               "\n  From here: pass replace_finished=True to run this anyway, "
                               "or call workspace.clear_finished() first — both refuse a run "
                               "that is still going.") from None
            self.clear_finished()
            started = self._t.request("POST", "/v1/runs", body, timeout=_LAUNCH_TIMEOUT_S)
        return self._await_outcome(started, opts)

    def _await_outcome(self, started: dict[str, Any], opts: dict[str, Any]) -> Outcome:
        try:
            out = self._wait(started, opts)
        except (RunTimeout, ApiError):
            raise
        except Exception as cause:  # noqa: BLE001 — the run exists whatever this was
            raise WaitError(started, cause) from cause
        self._delivered = started.get("id")
        return out

    def _wait(self, started: dict[str, Any], opts: dict[str, Any]) -> Outcome:
        run_id = started["id"]
        self._in_flight = run_id
        self._abort.clear()
        asked = opts.get("timeout")
        # `is None`, not truthiness: timeout=0 means "do not wait", and reading it
        # as unset made that the 30-minute default.
        budget = DEFAULT_RUN_TIMEOUT_S if asked is None else float(asked)
        deadline = time.monotonic() + budget
        delay = _POLL_MIN_S
        stopped = False
        try:
            while True:
                run = self._t.request("GET", f"/v1/runs/{run_id}")
                if run.get("state") in _FINISHED:
                    successor = self._failover_of(run_id, run)
                    if successor is not None:
                        # The daemon routed to another agent: it renamed the
                        # failed container and started a new one. Returning the
                        # first attempt's outcome would credit the agent that
                        # failed and leave the retry running — and the next run
                        # on this branch would then 409 on a live container this
                        # object cannot clear.
                        run_id = successor
                        self._in_flight = run_id
                        continue
                    return self._outcome(run, stopped)
                if self._abort.is_set():
                    # The async face was cancelled. It has stopped the run; this
                    # thread stops polling rather than holding the interpreter
                    # open until the deadline.
                    return self._outcome(run, True)
                if time.monotonic() >= deadline:
                    if stopped:
                        return self._outcome(run, True)
                    # The stop is what makes a deadline mean something, so its
                    # failure cannot be swallowed: reporting stopped after a
                    # refused stop would claim the container ended while it is
                    # still holding its name.
                    self.stop(run_id)
                    stopped = True
                self._abort.wait(delay)
                delay = min(delay * 1.6, _POLL_MAX_S)
        finally:
            self._in_flight = None

    def _failover_of(self, run_id: str, finished: dict[str, Any]) -> str | None:
        """The run the daemon started to replace this one, if it did.

        Only asked when a run ends non-zero and a fallback was possible, because
        it costs a listing. The link is `routedFrom`, which the daemon stamps on
        the replacement — the same field the audit line and the container label
        carry, so this reads the record rather than guessing from timing.
        """
        if finished.get("exitCode") in (0, None):
            return None
        try:
            runs = (self._t.request("GET", f"/v1/runs?all=1&repo={self.project.id}") or {}).get("runs") or []
        except ApiError:
            return None
        for r in runs:
            if r.get("routedFrom") == run_id or r.get("routed_from") == run_id:
                return r.get("id")
        return None

    def _outcome(self, run: dict[str, Any], stopped: bool) -> Outcome:
        lines = self.logs(run["id"])
        def stream(name: str) -> str:
            return "\n".join(l.get("text", "") for l in lines if l.get("stream") == name)
        return Outcome(
            id=run["id"],
            exit_code=run.get("exitCode", -1),
            stdout=stream("stdout"),
            stderr=stream("stderr"),
            stopped=stopped,
            agent=run.get("agent"),
            routed_from=run.get("routedFrom"),
            route_reason=run.get("routeReason"),
            run=run,
        )


# What GitHub actually allows in an owner or repository name. Narrow on purpose:
# the shorthand exists to save typing a known URL, and anything it does not
# recognise is passed through for the daemon to accept or refuse. `./local` is
# the case that made this a regex — as a prefix test it became
# `https://github.com/./local.git`, which is a URL nobody typed.
_GITHUB_SHORTHAND = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?/"
                               r"[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$")


def _expand_repo_url(url: str) -> str:
    """`owner/repo` means GitHub; everything else is passed through untouched."""
    trimmed = url.strip()
    if _GITHUB_SHORTHAND.match(trimmed):
        return f"https://github.com/{trimmed}.git"
    return trimmed


def _looks_like_path(value: str) -> bool:
    return value.startswith((".", "/", "~")) or "/" in value or "\\" in value
