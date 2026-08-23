"""Answering "which repository am I in?" from the machine running the script.

Every other path in this package belongs to the daemon. This module is the
exception, and it is kept separate so that stays visible: what it produces is a
fact about *this* machine, offered to the daemon as a guess and never trusted as
an answer. A daemon on another host resolves the same string against its own disk
and refuses, which is the correct outcome.
"""

from __future__ import annotations

import os
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path

# Mirrors internal/githard: rev-parse runs no filter, hook or textconv, so this
# is not the vector that package was written for — but the working directory here
# can be a tree an agent has been writing to, and saying so in advance is cheap.
_HARDENED = [
    "-c", "core.hooksPath=/dev/null",
    "-c", "core.attributesFile=/dev/null",
    "-c", "core.fsmonitor=",
    "-c", "core.sshCommand=",
    "-c", "core.askPass=",
    "-c", "credential.helper=",
    "-c", "core.pager=cat",
    "-c", "core.editor=false",
    "-c", "core.alternateRefsCommand=",
]


@dataclass(frozen=True)
class LocalRepo:
    """Where a script is standing, in the two forms that answer different questions."""

    root: str
    """The repository as the daemon records it: the main checkout for a linked
    worktree, the submodule's own tree for a submodule, the repository itself for
    a bare one."""

    tree: str
    """The working tree the script is actually in — the worktree rather than the
    main checkout when those differ, empty for a bare repository. Kept because a
    daemon started *inside* a worktree registers its default project as that
    worktree, so a lookup that only knew the main root would miss the one
    repository that cannot be removed."""


def _git(cwd: str, *args: str, timeout: float = 10.0) -> str | None:
    try:
        out = subprocess.run(["git", *_HARDENED, *args], cwd=cwd, timeout=timeout,
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=True)
    except (OSError, subprocess.SubprocessError):
        return None
    return out.stdout.decode().strip() or None


def local_repo(from_dir: str) -> LocalRepo | None:
    """The repository containing `from_dir`, asked of git the way the daemon asks.

    `--git-common-dir` alone is not the answer, and the TypeScript client learned
    that the expensive way: for a **submodule** it names `<super>/.git/modules/
    <name>`, and for `--separate-git-dir` the detached git directory. Registering
    either hands `/workspace` an object store instead of the source. So the rule
    covers all four layouts: `<x>/.git` means a checkout or a worktree of `<x>`;
    anything else means the git directory is not beside its tree and git is asked
    to name the tree; only when there is no tree — a bare repository — is the git
    directory the answer.
    """
    path = _realpath(os.path.abspath(from_dir))
    if not os.path.exists(path):
        return None
    common = _git(path, "rev-parse", "--path-format=absolute", "--git-common-dir")
    if common:
        tree = _git(path, "rev-parse", "--path-format=absolute", "--show-toplevel") or ""
        if os.path.basename(common) == ".git":
            return LocalRepo(_realpath(os.path.dirname(common)), _realpath(tree) if tree else "")
        if tree:
            return LocalRepo(_realpath(tree), _realpath(tree))
        return LocalRepo(_realpath(common), "")
    walked = _walk_up(path)
    return LocalRepo(walked, walked) if walked else None


def git_root_of(from_dir: str) -> str | None:
    found = local_repo(from_dir)
    return found.root if found else None


def init_repo(directory: str) -> None:
    """`git init` in a directory that is not in a repository yet.

    On **this** machine, which is the whole reason the caller has to ask for it:
    a path names a directory on the daemon's host, and against a remote daemon
    initialising here creates a repository nobody will ever see while the
    registration still fails.

    `init.templateDir=` because a template directory can carry hooks, and this is
    the one git command here that writes.
    """
    if not os.path.isdir(directory):
        raise FileNotFoundError(f"{directory} does not exist, so there is nothing to initialise")
    if _git(directory, "-c", "init.templateDir=", "init", "-q", timeout=30.0) is None:
        # `git init -q` prints nothing on success, so None is ambiguous — check.
        if local_repo(directory) is None:
            raise RuntimeError(f"git init failed in {directory}")


def unborn_with_files(directory: str) -> bool:
    """Whether a *repository* has files but no commits — the state in which every
    worktree Studio makes is **empty**.

    Three questions in order, because collapsing them produces a false sentence.
    Is git here at all? If not this cannot be asked, and the daemon will answer
    it on its own machine. Is this a repository? If not, "no commits yet" is not
    the problem and saying so sends somebody to run `git commit` in a directory
    with nothing to commit to. Only then: does HEAD resolve?

    The first version asked only the last question, and `_git` returns None for
    "git failed" and "git is missing" alike — so a plain directory with a file in
    it was refused as "a git repository with no commits yet", advice that cannot
    be followed.

    Reported rather than fixed. `git add -A && git commit` is one line to type and
    a bad thing to do *for* somebody: a directory that has never been a repository
    usually has no .gitignore, which is exactly where node_modules, a .env and a
    stray key get committed.
    """
    if shutil.which("git") is None:
        return False
    if local_repo(directory) is None:
        return False
    if _git(directory, "rev-parse", "--verify", "--quiet", "HEAD") is not None:
        return False
    try:
        return any(e.name != ".git" for e in os.scandir(directory))
    except OSError:
        return False


def wire_path(path: str, cwd: str) -> str:
    """A path as the daemon should receive it: `~` expanded, and absolute.

    Deliberately **not** symlink-resolved. That resolution is a fact about this
    machine, and applying it to a value bound for another one rewrites what the
    user typed: on macOS `/tmp/api` becomes `/private/tmp/api`, and a Linux
    daemon refuses a path nobody named.
    """
    expanded = os.path.expanduser(path)
    return expanded if os.path.isabs(expanded) else os.path.abspath(os.path.join(cwd, expanded))


def same_path(a: str, b: str) -> bool:
    """Whether two local paths name the same directory, symlinks included. Both
    sides are on this machine, which is where a realpath means something."""
    return _realpath(a) == _realpath(b)


def _walk_up(start: str) -> str | None:
    """The fallback for a machine with no git: a `.git` anywhere above. Tested for
    existence rather than as a directory, because a linked worktree's is a file."""
    current = start
    while True:
        if os.path.exists(os.path.join(current, ".git")):
            return current
        parent = os.path.dirname(current)
        if parent == current:
            return None
        current = parent


def _realpath(path: str) -> str:
    """realpath, falling back to the input: a path that does not exist here is not
    an error — it may exist on the daemon's machine, which is the one that decides."""
    try:
        return str(Path(path).resolve(strict=True))
    except OSError:
        return path
