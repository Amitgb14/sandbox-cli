"""What this client promises, checked against a stand-in daemon."""

from __future__ import annotations

import os
import subprocess
import tempfile
from pathlib import Path

import pytest
from fake_daemon import FakeDaemon

from sandbox_cli import ApiError, DaemonUnreachable, Studio


def _git_repo(directory: Path, *, commit: bool = True) -> Path:
    def git(*args: str) -> None:
        subprocess.run(["git", "-c", "init.templateDir=", "-c", "user.email=a@b",
                        "-c", "user.name=a", "-c", "commit.gpgsign=false", *args],
                       cwd=directory, check=True, stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL)
    git("init", "-q", "-b", "main")
    if commit:
        (directory / "README.md").write_text("x\n")
        git("add", "README.md")
        git("commit", "-qm", "init")
    return directory


def test_a_run_posts_the_repository_and_worktree_it_belongs_to():
    with FakeDaemon() as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        out = ws.run(["npm", "test"], env={"CI": "true"})
        assert out.exit_code == 0
        assert out.stdout == "hi"
        assert d.posted("/v1/runs")[0]["body"] == {
            "command": ["npm", "test"], "repo": "repo-1",
            "worktree": "feature", "branch": "feature", "env": {"CI": "true"}}


def test_the_token_travels_as_a_bearer_header():
    with FakeDaemon() as d:
        Studio.connect(url=d.url, token="secret").health()
        assert d.requests[0]["auth"] == "Bearer secret"


def test_a_second_step_clears_only_the_run_this_object_delivered():
    # The rule that broke the TypeScript client on line two of every script: a
    # finished run keeps the branch's container name, so `run()` twice was
    # refused. Clearing the run whose outcome was already handed back discards no
    # evidence anybody is waiting for.
    with FakeDaemon(holds_name_after_run=True) as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        assert ws.run(["echo", "one"]).exit_code == 0
        assert ws.run(["echo", "two"]).exit_code == 0
        assert d.removed == ["run-1"]


def test_somebody_elses_finished_run_is_still_refused():
    # Nothing was delivered by this object, so there is nothing it may clear: the
    # holder belongs to an earlier session and its logs are evidence this client
    # never received.
    with FakeDaemon() as d:
        d._held = "abc123"  # a run from before this process started
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        with pytest.raises(ApiError) as caught:
            ws.run(["echo", "hi"])
        assert "still holds" in str(caught.value)
        assert "replace_finished=True" in str(caught.value)
        assert d.removed == []


def test_an_ambiguous_name_is_refused_rather_than_picked():
    with FakeDaemon() as d:
        with pytest.raises(ValueError, match="use an id"):
            Studio.connect(url=d.url, token="t").project("twin")


def test_a_missing_daemon_says_where_it_looked():
    with pytest.raises(DaemonUnreachable, match="no Studio daemon answered"):
        Studio.connect(url="http://127.0.0.1:9", token="t").health()


def test_the_repository_here_is_found_by_root(tmp_path, monkeypatch):
    repo = tmp_path / "repo"
    repo.mkdir()
    _git_repo(repo)
    with FakeDaemon(project_root=str(repo.resolve())) as d:
        (repo / "scripts").mkdir()
        monkeypatch.chdir(repo / "scripts")
        found = Studio.connect(url=d.url, token="t").project()
        assert found.root == str(repo.resolve())


def test_a_forgotten_argument_is_not_a_request_for_the_current_repository():
    with FakeDaemon() as d:
        with pytest.raises(ValueError, match="missing argument rather than a request"):
            Studio.connect(url=d.url, token="t").project("")


def test_add_project_expands_but_never_resolves_the_path(tmp_path, monkeypatch):
    with FakeDaemon() as d:
        monkeypatch.chdir(tmp_path)
        studio = Studio.connect(url=d.url, token="t")
        studio.add_project("/tmp/some-api")
        # /tmp is a symlink to /private/tmp on macOS. Resolving here would post a
        # path the user never typed, and against a Linux daemon one that does not
        # exist. The daemon resolves against its own disk; that is its job.
        assert d.posted("/v1/projects")[-1]["body"] == {"path": "/tmp/some-api"}
        studio.add_project("~/code/api")
        assert d.posted("/v1/projects")[-1]["body"] == {
            "path": os.path.join(os.path.expanduser("~"), "code", "api")}


def test_a_repository_with_files_and_no_commits_is_refused(tmp_path, monkeypatch):
    # The trap `git init` leaves: registerable, and every worktree Studio makes
    # from it is empty — the agent starts in a /workspace with none of the code.
    repo = tmp_path / "unborn"
    repo.mkdir()
    (repo / "main.py").write_text("print('hi')\n")
    with FakeDaemon() as d:
        monkeypatch.chdir(repo)
        studio = Studio.connect(url=d.url, token="t")
        with pytest.raises(ValueError) as caught:
            studio.add_project(init=True)
        assert "no commits yet" in str(caught.value)
        assert "git add -A && git commit" in str(caught.value)
        assert d.posted("/v1/projects") == [], "it must refuse before asking the daemon"


def test_init_creates_a_repository_only_when_asked(tmp_path, monkeypatch):
    empty = tmp_path / "fresh"
    empty.mkdir()
    with FakeDaemon() as d:
        monkeypatch.chdir(empty)
        studio = Studio.connect(url=d.url, token="t")
        with pytest.raises(ValueError, match="Pass init=True"):
            studio.add_project()
        assert not (empty / ".git").exists(), "the refusal must not have created anything"
        studio.add_project(init=True)
        assert (empty / ".git").exists()


def test_an_agent_run_needs_a_prompt():
    with FakeDaemon() as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        with pytest.raises(ValueError, match="the whole instruction"):
            ws.agent("claude", "   ")


def test_clone_expands_the_github_shorthand_and_nothing_else():
    with FakeDaemon() as d:
        studio = Studio.connect(url=d.url, token="t")
        studio.clone("Amitgb14/sandbox-cli", "/home/you/code")
        assert d.posted("/v1/projects/clone")[-1]["body"] == {
            "url": "https://github.com/Amitgb14/sandbox-cli.git", "parent": "/home/you/code"}
        # A URL is passed through untouched — including one the daemon must
        # refuse. Deciding that here would put the refusal in two places and let
        # them disagree.
        studio.clone("ext::sh -c whoami", "/home/you/code", name="evil")
        assert d.posted("/v1/projects/clone")[-1]["body"] == {
            "url": "ext::sh -c whoami", "parent": "/home/you/code", "name": "evil"}


def test_steps_stop_at_the_first_failure():
    # A loop that runs everything reports the *last* exit code, so a failed
    # install followed by a passing lint looks like success.
    with FakeDaemon() as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        d._fail_after = 1  # the stand-in exits non-zero from the second run on
        done = ws.steps([["a"], ["b"], ["c"]])
        assert [o.exit_code for o in done] == [0, 1]
        assert len(d.posted("/v1/runs")) == 2, "the third step must not have run"


def test_workspace_env_is_the_base_and_a_run_wins_per_key():
    with FakeDaemon() as d:
        repo = Studio.connect(url=d.url, token="t").project("app")
        ws = repo.workspace("feature", env={"CI": "true", "LOG": "info"})
        ws.run(["pytest"], env={"LOG": "debug"})
        assert d.posted("/v1/runs")[-1]["body"]["env"] == {"CI": "true", "LOG": "debug"}


def test_env_files_are_parsed_strictly(tmp_path):
    from sandbox_cli.env import read_env_file

    good = tmp_path / ".env"
    good.write_text('# comment\nexport TOKEN="abc"\nPLAIN=v\nQ=\'x y\'\n\n')
    assert read_env_file(good) == {"TOKEN": "abc", "PLAIN": "v", "Q": "x y"}

    # A silently skipped line in a credentials file is how a run goes out without
    # the key it needed.
    bad = tmp_path / "bad.env"
    bad.write_text("NOT A PAIR\n")
    with pytest.raises(ValueError, match="expected KEY=value"):
        read_env_file(bad)

    with pytest.raises(FileNotFoundError):
        read_env_file(tmp_path / "absent.env")
    assert read_env_file(tmp_path / "absent.env", missing_ok=True) == {}
