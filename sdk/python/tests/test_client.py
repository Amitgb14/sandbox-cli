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


def test_a_plain_directory_is_not_called_a_repository_without_commits(tmp_path):
    # "git could not answer" is not "unborn". The first version conflated them,
    # so a directory that was never a repository was refused as one with no
    # commits — advice that cannot be followed.
    plain = tmp_path / "plain"
    plain.mkdir()
    (plain / "file.txt").write_text("hi\n")
    with FakeDaemon() as d:
        studio = Studio.connect(url=d.url, token="t")
        studio.add_project(str(plain))          # the daemon decides, not this client
        assert d.posted("/v1/projects")[-1]["body"] == {"path": str(plain)}


def test_an_unknown_run_option_is_a_typo_not_a_preference():
    # One of these names is a security control: `alow=[...]` would launch with
    # the daemon's default egress posture and report success.
    with FakeDaemon() as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        with pytest.raises(TypeError, match="unknown run option"):
            ws.run(["echo", "hi"], alow=["api.example.com"])
        assert d.posted("/v1/runs") == []


def test_timeout_zero_means_do_not_wait():
    # Read as truthiness, `timeout=0` became the 30-minute default — the opposite
    # of what it asks for.
    with FakeDaemon(never_finishes=True) as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        out = ws.run(["sleep", "long"], timeout=0)
        assert out.stopped is True
        assert any(r["path"].endswith("/stop") for r in d.requests)


def test_the_token_env_var_matches_the_typescript_rule(monkeypatch):
    from sandbox_cli._discover import discover_token

    # An unset passthrough in a shell wrapper produces "", and treating that as a
    # token means every request 401s where the TypeScript client works.
    monkeypatch.setenv("SANDBOX_STUDIO_TOKEN", "")
    monkeypatch.setenv("XDG_CONFIG_HOME", "/nonexistent")
    assert discover_token() == ""
    monkeypatch.setenv("SANDBOX_STUDIO_TOKEN", "real")
    assert discover_token() == "real"


def test_a_failover_is_followed_rather_than_reported_as_the_failure():
    # The daemon renames the failed container and starts a new one. Returning the
    # first attempt would credit the agent that failed and leave the retry
    # running — and the next run on the branch would 409 on a live container.
    with FakeDaemon(failover=True) as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        out = ws.agent("claude", "do the thing", fallback=["codex"])
        assert out.id == "run-2", "the outcome should be the retry's"
        assert out.exit_code == 0
        assert out.agent == "codex"


def test_cancelling_an_async_run_stops_that_run_and_nothing_else():
    # Two failures the first version had, both reproduced by the review: it
    # searched for a live run on the branch — which on a busy branch is another
    # process's agent — and it left the worker thread polling to the deadline,
    # so a cancelled await held the interpreter open for up to thirty minutes.
    import asyncio

    from sandbox_cli import RunCancelled
    from sandbox_cli.aio import AsyncStudio

    async def scenario():
        with FakeDaemon(never_finishes=True) as d:
            studio = await AsyncStudio.connect(url=d.url, token="t")
            ws = await (await studio.project("app")).workspace("feature")
            task = asyncio.create_task(ws.run(["sleep", "long"], timeout=600))
            await asyncio.sleep(0.4)          # let the launch land
            task.cancel()
            started = asyncio.get_event_loop().time()
            try:
                await task
                raise AssertionError("the cancel should surface")
            except RunCancelled as cancelled:
                elapsed = asyncio.get_event_loop().time() - started
                stops = [r["path"] for r in d.requests if r["path"].endswith("/stop")]
                return cancelled.run["id"], stops, elapsed

    run_id, stops, elapsed = asyncio.run(scenario())
    assert run_id == "run-1", "it must name the run this call launched"
    assert stops == ["/v1/runs/run-1/stop"], f"it stopped the wrong thing: {stops}"
    # Promptly: one poll interval, not the 600s deadline the run was given.
    assert elapsed < 10, f"the cancel took {elapsed:.1f}s — the worker kept polling"


def test_start_returns_without_waiting():
    # For work that is not supposed to finish. `run()` would wait for the
    # deadline and then report a container somebody stopped, which is a verdict
    # on nothing.
    with FakeDaemon(never_finishes=True) as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        started = ws.start(["uvicorn", "app:app"], publish=["8000:8000"])
        assert started["id"] == "run-1"
        assert d.posted("/v1/runs")[-1]["body"]["publish"] == ["8000:8000"]
        # Nothing was polled: no GET on the run, because nothing was waited for.
        assert not any(r["method"] == "GET" and r["path"].startswith("/v1/runs/run-1")
                       for r in d.requests)


def test_start_clears_the_setup_run_that_holds_the_name():
    # The commonest sequence there is — set up, then serve — and it failed on its
    # last line until `start` shared `run`'s recovery: the final setup step still
    # held the branch's container name.
    with FakeDaemon(holds_name_after_run=True) as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        assert ws.run(["pip", "install", "-r", "requirements.txt"]).exit_code == 0
        started = ws.start(["uvicorn", "app:app"])
        assert started["id"] == "run-1"
        assert d.removed == ["run-1"], "the spent setup run should have been cleared"


def test_start_refuses_a_timeout_it_could_not_honour():
    # Accepted-and-ignored is the failure the option validator exists to prevent,
    # arriving with the spelling correct: start() does not wait, so a deadline
    # has nothing to bound.
    with FakeDaemon(never_finishes=True) as d:
        ws = Studio.connect(url=d.url, token="t").project("app").workspace("feature")
        with pytest.raises(TypeError, match="takes no timeout"):
            ws.start(["uvicorn", "app:app"], timeout=60)
        assert d.posted("/v1/runs") == [], "it must refuse before launching"


def test_cancelling_an_async_start_stops_the_container_it_created():
    # asyncio.to_thread cannot be interrupted, so a cancel during the POST does
    # not prevent the launch: it lands afterwards, in a thread nobody is
    # watching. Without the done-callback the run exists, holds its branch's
    # name, and no caller ever saw its id.
    import asyncio

    from sandbox_cli.aio import AsyncStudio

    async def scenario():
        with FakeDaemon(never_finishes=True, launch_delay=0.6) as d:
            studio = await AsyncStudio.connect(url=d.url, token="t")
            ws = await (await studio.project("app")).workspace("feature")
            task = asyncio.create_task(ws.start(["uvicorn", "app:app"]))
            await asyncio.sleep(0.2)          # cancel while the POST is in flight
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass                          # prompt, as a cancel should be
            await asyncio.sleep(1.5)          # let the launch land and be stopped
            return [r["path"] for r in d.requests if r["path"].endswith("/stop")]

    stops = asyncio.run(scenario())
    assert stops == ["/v1/runs/run-1/stop"], f"the late launch was not stopped: {stops}"
