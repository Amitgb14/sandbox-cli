# Agent reference

Every agent `sandbox-cli` wraps, what you need before you run it, and how to log
in from inside a container that has no browser.

> **Verification status.** The wiring below — flags, forwarded variables, mounts,
> persisted paths — is covered by the test suite and by `--dry-run`. The
> *installs* have not been executed in a real container: this repo's integration
> tests need a Docker daemon. Package names, install commands, config paths and
> login flows were each checked against upstream sources in July 2026, but the
> first person to run a given agent is the first to prove its install end to end.
> Treat sizes as approximate.

## What every wrapper does

All of them behave the same way, so learn it once:

```sh
sandbox-cli <agent> [sandbox flags] [-- ] [everything else goes to the agent]
```

- **Your arguments are forwarded verbatim.** `sandbox-cli claude --dangerously-skip-permissions`
  just works. A leading run of sandbox long-flags is consumed by sandbox; the
  first token that isn't one ends the sandbox portion. See
  [Passing flags to the agent](usage/flags.md#passing-flags-to-the-agent).
- **The login persists, once.** Each agent gets its own sandbox-owned directory
  (`~/.config/sandbox/agents/<agent>`) bind-mounted as the container's whole
  `HOME`. It is **separate from your real config** — the sandbox never reads or
  writes your host `~/.claude`, `~/.gemini`, `~/.factory`, etc. `--no-persist-auth`
  gives you a throwaway session instead.
- **Host env vars are opt-in.** Each agent has a small allowlist of variables
  forwarded *only if they are set on your host*. Nothing else crosses. Add more
  per run with `--env-allow NAME`, or set one outright with `--env K=V`.
- **Path-valued variables are deliberately never forwarded.** Almost every agent
  has one that relocates its state directory (`CLINE_DATA_DIR`, `GOOSE_PATH_ROOT`,
  `COPILOT_HOME`, `QWEN_HOME`, `AMP_HOME`, `FACTORY_HOME_OVERRIDE`, …). The host
  path it names is not mounted, so forwarding it would move the login somewhere
  the container cannot see and silently cost you the session on every run.
- **Only `/workspace` is host-connected.** Anything else needs an explicit
  `--mount`.
- **The conversations survive too.** Because the agent home persists, so do the
  transcripts the agent writes into it — `sandbox-cli <agent> context list` shows
  them with the id you resume by. See
  [Seeing an agent's past conversations](#seeing-an-agents-past-conversations).

## Prerequisites shared by all agents

1. **Docker** running (Docker Desktop on macOS/Windows).
2. **`sandbox-cli` installed** — see [Install](install.md).
3. **An account or API key for the agent you want.** The sandbox does not supply
   credentials; it only isolates the agent that uses them.
4. **Network on an agent's first run** if it isn't baked into the image (below).

### Baked in vs installed on first use

Four agents ship in the base image. Everything else installs itself into the
persisted agent home the first time you run it — so the image stays small and you
only download agents you actually use. That first run takes a while and needs
network; later runs start immediately.

| Agent | Availability | Installed size (approx.) |
|---|---|---|
| `claude`, `codex`, `gemini`, `opencode` | baked into the base image | — |
| `copilot` | on first use | 350 MB |
| `goose` | on first use | 273 MB |
| `cursor` | on first use | 219 MB |
| `droid` | on first use | 148 MB |
| `cline` | on first use | 130 MB |
| `amp` | on first use | 107 MB |
| `qwen` | on first use | 88 MB |
| `openhands` | on first use | 82 MB |
| `crush` | on first use | 81 MB |
| `continue` | on first use | 65 MB |
| `aider` | on first use | ~300 MB (uv + Aider's Python deps; not measured) |

These are the **on-disk installed** sizes for the arm64 build, measured in July
2026 — not the compressed download, which is roughly half. x64 is within a few
percent. Every figure except `aider` was verified by installing the artifact and
measuring it: the npm agents from the platform payload the stub package pulls,
and `goose`/`cursor`/`openhands`/`crush` by downloading and extracting the
release. `aider` installs a chain of Python wheels through uv whose total is hard
to pin without running it, so its number is an estimate and flagged as such.

Two notes on the larger ones. `goose` ships a glibc ("standard", 273 MB) and a
smaller musl (136 MB) build; on the glibc base image the installer picks the
standard one automatically, which is the number shown. `copilot` is the biggest
at 350 MB and the one most likely to look like a hang on first run — it is not,
it is downloading.

If an install fails you get an explicit message and exit code 127 — not a
mysterious "command not found".

### Which version gets installed

That first-run install is **pinned to a version sandbox-cli records**, not to
whatever the vendor is publishing that day. The version is announced when it
installs:

```
sandbox-cli: installing qwen 0.21.3 into the sandbox agent home (first run only)...
```

The pins live in one table, `internal/agents/pins.go`, and that is the only place
to change one. The reason is the ordinary supply-chain attack — a hijacked or
typosquatted release — which reaches you at first install and nowhere else,
because that install is the only one sandbox-cli controls. It does **not** protect
against a compromised registry serving different bytes for a version it already
published; that needs integrity hashes a global npm install has no lockfile for.

Two consequences worth knowing:

- **A pin can go stale.** An agent that does not update itself stays on the
  recorded version until the pin is bumped. That is why the version is printed
  rather than installed silently — if an agent seems old, the number on that line
  is the thing to report.
- **Self-updating agents are unaffected after the first run.** The bootstrap
  prefers an existing binary in the persisted agent home and execs it directly, so
  an agent that updates itself keeps doing so; the pin only decides where it
  starts.

Two agents are deliberately **not** pinned, and the table says why in each case:
`cursor`, because `cursor.com/install` regenerates its script per release with the
version baked in and offers no way to ask for another; and `claude`, because
Claude Code updates itself into the persisted home — which is why that home exists
— so a pin would govern only a first install it immediately replaces.

---

# The agents

## claude — Claude Code

- **Prerequisites:** a Claude account (Pro/Max) or `ANTHROPIC_API_KEY`.
- **Setup:** run it and follow the login prompt. Credentials land in the agent
  home and are reused after that.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
  `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`.
- **Extras unique to this wrapper:** a live memory/CPU status line in Claude's own
  UI, which also names the model in play and shows how much of your 5-hour and
  weekly subscription windows is spent and when they reset (`--no-statusline` to
  disable the line, `--env SANDBOX_STATUSLINE_NO_USAGE=1` /
  `--env SANDBOX_STATUSLINE_NO_MODEL=1` to keep it without one of them; Claude Code
  reports the windows only on a Claude.ai plan and only after its first request, so
  under API-key auth they are absent). `sandbox-cli usage` shows the same windows
  outside a Claude session, including any per-model cap your plan meters
  separately. And your **host Claude history for this
  project** is shared by default so a host session can be `--resume`d inside the
  container and vice versa (`--no-sync` to keep them separate). It is also the
  one agent whose transcripts sandbox-cli reads in full, so
  `sandbox-cli claude context list` shows each session's title and how many
  prompts you sent.

```sh
sandbox-cli claude
sandbox-cli claude --dangerously-skip-permissions
sandbox-cli claude --worktree feature-a -- -p "implement the API"
```

## codex — Codex CLI

- **Prerequisites:** a ChatGPT account or `OPENAI_API_KEY`.
- **Setup:** `sandbox-cli codex`, then pick **`Sign in with Device Code`** at the
  sign-in menu. `Sign in with ChatGPT` is **not supported here** — it finishes
  through a login server on the container's own loopback, which no amount of
  publishing can expose ([why](#browser-callback-logins-are-not-supported)).
  Codex hints at this on screen: "On a remote or headless machine?". Exporting
  `OPENAI_API_KEY` on your host skips the menu entirely.
- **Forwarded if set:** `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `CODEX_HOME`.

```sh
sandbox-cli codex
sandbox-cli codex exec 'run the tests'
```

## gemini — Gemini CLI

- **Prerequisites:** `GEMINI_API_KEY` (simplest), or a Google account for OAuth,
  or a Vertex AI project.
- **Setup:** with no key, Gemini prints a Google sign-in URL — open it on your
  **host**; the credentials land in the persisted agent home. Forwarding
  `GEMINI_API_KEY` skips the step entirely.
- **Forwarded if set:** `GEMINI_API_KEY`, `GOOGLE_API_KEY`,
  `GOOGLE_GENAI_USE_VERTEXAI`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`.
- **Note:** `GOOGLE_APPLICATION_CREDENTIALS` is *not* forwarded — it names a host
  file that isn't mounted. To use it, mount the file and point at the new path:

```sh
sandbox-cli gemini --mount ~/adc.json:/sandbox/home/adc.json:ro \
  --env GOOGLE_APPLICATION_CREDENTIALS=/sandbox/home/adc.json
```

## opencode — OpenCode

- **Prerequisites:** an API key for any provider it supports.
- **Setup:** `opencode auth login` inside the sandbox, or forward a provider key.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`,
  `GROQ_API_KEY`, `OPENROUTER_API_KEY`, `OPENCODE_CONFIG`,
  `OPENCODE_DISABLE_AUTOUPDATE`.

```sh
sandbox-cli opencode                      # interactive
sandbox-cli opencode run 'run the tests'  # non-interactive
```

**`run` is not optional for a prompt.** A bare positional is read by opencode as
*the project directory to open*, so `sandbox-cli opencode 'review the code'`
fails with `Failed to change directory to /workspace/review the code`. The same
is true of a Studio **console** run: opencode cannot be seeded with a first turn,
so Studio refuses that combination and points at the headless mode, where the
prompt is spelled correctly.

**xAI / SuperGrok:** choose **`xAI Grok OAuth (Headless / Remote / VPS)`**. It is
a device-code flow — a short code and a URL you open on any device — and it is
the only xAI browser login that works here. `xAI Grok OAuth (SuperGrok
Subscription)` is **not supported**: it finishes through a fixed
`http://127.0.0.1:56121/callback`, a redirect URI registered with xAI and so not
repointable, served inside the container where your host browser cannot reach it
([why](#browser-callback-logins-are-not-supported)). With `--allow`, add
`auth.x.ai` (login and token refresh) and `api.x.ai` (inference).

## cline — Cline

- **Prerequisites:** a provider API key, or a Cline account.
- **Setup — non-interactive (recommended here):**
  `cline auth --provider anthropic --apikey sk-...`, or forward a key.
  With an OAuth provider and no stored credentials, Cline **fails with an auth
  message rather than opening a browser** — that is intended behaviour, not a
  crash.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `CLINE_API_KEY`, `OPENAI_API_KEY`,
  `OPENROUTER_API_KEY`, `AI_GATEWAY_API_KEY`, `V0_API_KEY`.

```sh
sandbox-cli cline
sandbox-cli cline 'run the tests'
```

## goose — Goose

- **Prerequisites:** a provider API key.
- **Setup:** `sandbox-cli goose` then `goose configure` (an interactive TUI — no
  browser involved), or forward a provider key.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`,
  `GROQ_API_KEY`, `OPENROUTER_API_KEY`, `GOOSE_PROVIDER`, `GOOSE_MODEL`,
  `GOOSE_FAST_MODEL`, `GOOSE_MODE`.
- **The sandbox sets `GOOSE_DISABLE_KEYRING=1` for you.** Goose stores secrets in
  the OS keyring by default and a container has none, so without this the login
  would not survive. Secrets go to `~/.config/goose/secrets.yaml` in the persisted
  home instead. Don't override it.

```sh
sandbox-cli goose
sandbox-cli goose run -t 'run the tests'
```

## crush — Crush

- **Prerequisites:** a Charm account, or any supported provider key.
- **Setup:** `crush login` shows a short code — open the page on your **host** and
  paste it. No browser or localhost callback needed in the container.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`,
  `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `HYPER_API_KEY`, plus AWS/Azure keys.
  Crush supports ~25 providers; add any other with `--env-allow`.

```sh
sandbox-cli crush
sandbox-cli crush --env-allow CEREBRAS_API_KEY
```

## aider — Aider

- **Prerequisites:** a provider API key, **and the workspace must be a git repo**.
- **Setup:** none — Aider has no login at all. Export a key on your host.
- **Forwarded if set:** `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`,
  `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`, `OPENAI_API_BASE`,
  `ANTHROPIC_API_BASE`.
- **It writes into your project.** On first run Aider creates
  `.aider.chat.history.md` and a tags cache, and **appends `.aider*` to your
  repo's `.gitignore`**. Pass `--no-gitignore` if you'd rather it didn't.
- Because there is no login, `--no-persist-auth` costs you almost nothing here
  (only the cached install and chat history).

```sh
OPENAI_API_KEY=... sandbox-cli aider
sandbox-cli aider --no-gitignore
```

## copilot — GitHub Copilot CLI

- **Prerequisites:** an active **GitHub Copilot subscription**.
- **Setup:** `copilot login` prints a code for github.com/login/device — enter it
  on your **host**. Copilot then asks whether to store the token in its config
  file, because a container has no OS keychain. **Answer yes**: that file is in
  the sandbox-owned agent home, and it is what makes the login persist.
- **Forwarded if set:** `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`,
  `GH_HOST`, `COPILOT_MODEL`, `COPILOT_API_URL`.
- **⚠️ Think before forwarding a GitHub token.** Provider API keys buy the
  container inference. A GitHub PAT reaches **every repository you can** — far
  beyond the workspace. It's forwarded only if set, so leave it unset to use the
  device flow instead.

```sh
sandbox-cli copilot
sandbox-cli copilot -p 'run the tests'
```

## cursor — Cursor CLI

- **Prerequisites:** a Cursor account, or `CURSOR_API_KEY`.
- **Setup:** `cursor-agent login` prints a URL — open it on your **host**. It
  polls for the result; nothing listens on localhost.
- **Forwarded if set:** `CURSOR_API_KEY`, `CURSOR_API_ENDPOINT`.
- **The sandbox sets `NO_OPEN_BROWSER=1`** so it doesn't attempt a launch that can
  only fail.
- **If it complains about its own sandboxing**, pass `--sandbox disabled` — this
  container is already providing the isolation that feature exists for.

```sh
sandbox-cli cursor
sandbox-cli cursor --project ~/app -- --sandbox disabled
```

## qwen — Qwen Code

- **Prerequisites:** an API key — DashScope/Bailian, or any OpenAI-compatible
  endpoint. **Qwen's own OAuth free tier was discontinued**, so despite what older
  guides say, plan on a key.
- **Setup:** forward a key, or enter one with `/auth` inside the agent.
- **Forwarded if set:** `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`,
  `GOOGLE_API_KEY`, `DASHSCOPE_API_KEY`, `OPENROUTER_API_KEY`,
  `BAILIAN_CODING_PLAN_API_KEY`, `OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`,
  `OPENAI_MODEL`.
- **The sandbox sets `SANDBOX=1` and `NO_BROWSER=1`.** Qwen is a Gemini CLI fork
  and will otherwise try to re-run itself inside a container it starts via docker
  — impossible here, and it fails *after* startup, which is a confusing place to
  find out. `SANDBOX=1` tells it what is already true.

```sh
DASHSCOPE_API_KEY=... sandbox-cli qwen
```

## amp — Amp

- **Prerequisites:** an Amp account, or `AMP_API_KEY` from ampcode.com.
- **Setup:** `amp login` prints a URL to open on your **host** and takes the code
  back in the terminal. `AMP_API_KEY` skips it.
- **Forwarded if set:** `AMP_API_KEY`, `AMP_URL`, `AMP_LOG_LEVEL`,
  `AMP_SKIP_UPDATE_CHECK`.
- **Leave the native-keyring setting off.** Turning it on migrates Amp's token
  file into a keyring and deletes the file — in a container that trades a working
  login for none. The default (file store) is correct here.

```sh
sandbox-cli amp
sandbox-cli amp -x 'run the tests'
```

## continue — Continue CLI

- **Prerequisites:** `ANTHROPIC_API_KEY`.
- **Setup:** none. **There is no login** — hub authentication was removed
  upstream, so `cn login` and `CONTINUE_API_KEY` in the published docs are both
  stale and do nothing. The key is written into the config in the agent home on
  first use.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `CONTINUE_API_BASE`, AWS keys,
  `GOOGLE_CLOUD_PROJECT`.
- **With `--allow`, permit `api.continue.dev`** — with no config yet, Continue
  fetches a default one from there and otherwise has nothing to configure itself
  from.

```sh
ANTHROPIC_API_KEY=... sandbox-cli continue
sandbox-cli continue --allow api.continue.dev
```

## openhands — OpenHands CLI

- **Prerequisites:** an OpenHands Cloud account, or an LLM API key.
- **Setup:** `openhands login` is a device-code flow — open the URL on your
  **host** and enter the code.
- **Forwarded if set:** `LLM_API_KEY`, `LLM_MODEL`, `LLM_BASE_URL`,
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENHANDS_CLOUD_URL`.
  **`LLM_*` only take effect if you also pass `--override-with-envs`** — that's
  OpenHands' rule, and it's why an exported key can look ignored.
- **Two tools are degraded here, by design of the environment:** its terminal tool
  prefers tmux and falls back to plain subprocesses without it, and its browsing
  tool needs a browser the image doesn't carry. Neither is fatal.
- OpenHands is known for running its own runtime container per session. That's
  `openhands serve` (the web GUI), which this wrapper does not run — the CLI works
  against the local workspace, so no docker socket is needed.

```sh
sandbox-cli openhands
LLM_API_KEY=... LLM_MODEL=... sandbox-cli openhands -- --override-with-envs
```

## droid — Droid (Factory)

- **Prerequisites:** a Factory account, or `FACTORY_API_KEY`.
- **Setup:** login is a device-code flow — code and URL printed, opened on your
  **host**. `FACTORY_API_KEY` skips it, which is the usual choice for
  `droid exec`.
- **Forwarded if set:** `FACTORY_API_KEY`, `FACTORY_API_BASE_URL`,
  `FACTORY_APP_BASE_URL`, `FACTORY_AIRGAP_ENABLED`, `FACTORY_ENV`.
- **The sandbox sets `FACTORY_DISABLE_KEYRING=1`** so credentials stay in a file
  in the persisted home, even if the upstream default changes.

```sh
sandbox-cli droid
sandbox-cli droid exec 'run the tests'
```

---

## Seeing an agent's past conversations

Every wrapper persists the agent's `HOME`, so the sessions it records outlive the
disposable container. `context list` shows them, newest first, with the id you
resume by — and ends with the exact command that resumes the newest one:

```sh
sandbox-cli context list          # every agent that has a verified session store
sandbox-cli claude context list   # one agent; the same command, spelled per agent
sandbox-cli context list --all    # every project, not just the one you're in
sandbox-cli context list --full   # whole ids (-f), for resuming outside sandbox-cli
```

`--limit 0` lists everything (the default stops at 20 and says how many it held
back), `--json` is for scripts, and `-v` adds where each agent's store is.

Four agents have a store that has actually been located; every other adapter is
reported `untracked` rather than guessed at, because a wrong path would make "you
have no sessions" look like an answer instead of a gap:

| Agent | What the listing can read | Where the sessions are |
|---|---|---|
| `claude` | everything — title and prompt count | your real `~/.claude/projects/<project>`, since that history is mounted by default; the agent home instead under `--no-sync` |
| `codex` | id and date; `?` for title and count | `~/.config/sandbox/agents/codex/.codex/sessions` |
| `gemini` | id and date; `?` for title and count | `~/.config/sandbox/agents/gemini/.gemini/tmp` |
| `opencode` | id and date; `?` for title and count | `~/.config/sandbox/agents/opencode/.local/share/opencode/storage/session` |
| everything else | nothing yet | reported `untracked` |

Two things worth knowing before you resume:

- **Only `claude`'s store is organised by project**, so only its listing can be
  narrowed to the directory you're standing in. The others are listed whole and
  say so on the last line, rather than passing one agent's entire history off as
  this project's.
- **Ids are abbreviated**, and sandbox-cli expands one back to the full id before
  the agent sees it — but only when exactly one session in this project matches;
  an ambiguous or unknown value is forwarded untouched. Resuming *outside*
  sandbox-cli needs the whole id, so use `-f`. Agents also spell resume
  differently (`claude --resume <id>` is a flag, `codex resume <id>` is a
  subcommand); the listing prints the right form for the agent you asked about.

When there is nothing to list, the output says which directories were searched —
so "where does this agent even keep its sessions?" is answered where the question
comes up.

## Running an agent detached

`--detach` starts the sandbox in the background and prints its container name, so
one terminal can launch several agents (see
[GUIDE.md](GUIDE.md#in-the-background) for the full cycle). Two
things decide whether an adapter can be used that way:

1. **It needs a non-interactive mode**, because nothing is attached to answer a
   prompt. The five with a verified one are in the table below; for any other
   adapter, check its own `--help` before detaching it.
2. **Log in — and, for an adapter installed on first use, install — before
   detaching.** Both are interactive by nature and both persist afterwards, so
   one foreground run per agent is enough:

   ```sh
   sandbox-cli aider            # logs in, and installs on the first run
   sandbox-cli claude --worktree feature-a --detach -- -p "implement A"
   ```

   Skipping this makes the first detached run do the download unattended: it
   needs network at that moment, the install host has to be on the allowlist
   under `--allow` (see the table below), and a failure shows up only as exit
   127 in `docker logs`.

## Agents a fleet can run

A [fleet](GUIDE.md#a-fleet) starts every agent detached, so an adapter may only
appear in a `fleet.yaml` if it has a **verified headless mode** — a way to run a
prompt to completion without ever asking a human anything. An agent that stops
for approval in a fleet does not fail; it hangs until you notice, holding a slot.

| `agent:` | What the fleet runs | Notes |
|---|---|---|
| `claude` | `claude -p PROMPT --dangerously-skip-permissions` | baked into the image |
| `cline` | `cline PROMPT --auto-approve true` | installed on first run; the prompt is a bare positional and the TUI is the opt-in (`-i`), which is the inverse of the others |
| `codex` | `codex exec PROMPT` | baked into the image; Codex applies its own approval policy on top — relax it through the task's `args:` |
| `gemini` | `gemini --yolo -p PROMPT` | baked into the image; `-p` alone still stops for tool approval, which is why `--yolo` is not optional here |
| `opencode` | `opencode run PROMPT` | baked into the image |
| `droid` | `droid exec PROMPT` | installed on first use (~148MB) |

Anything else is rejected when the file is parsed, before a single container
starts. The other nine adapters are perfectly usable interactively — they are
simply not ones we have confirmed will never stop and wait.

Two things to do before an unattended run, and they are per agent rather than per
fleet — **a fleet that mixes agents needs each one set up**:

```sh
sandbox-cli claude      # log in (and install, for a lazily-installed adapter)
sandbox-cli codex       # …and again for the second agent in the file
```

`sandbox-cli fleet run --dry-run` prints a reminder when it sees a file naming
more than one agent. Under `--profile prod` the persisted login is not mounted at
all, so each agent needs its key in the environment instead
(`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, …).

Adding an agent to this list means verifying its headless mode by running it —
the argv is recorded in `internal/agents` and pinned by a test, so a new
descriptor without a verified invocation fails the build rather than quietly
widening what a fleet may name.

## Using agents with `--allow` (egress allowlist)

`--allow` switches the container to a default-deny firewall. These are always
permitted:

```
api.anthropic.com  api.openai.com  registry.npmjs.org  pypi.org
files.pythonhosted.org  github.com  codeload.github.com
objects.githubusercontent.com  raw.githubusercontent.com
```

So npm-installed agents (`cline`, `crush`, `copilot`, `qwen`, `amp`, `continue`,
`droid`) and the GitHub-released ones (`goose`, `openhands`) can install with the
baseline alone. These need more:

| Agent | Add to `--allow` | Why |
|---|---|---|
| `cursor` | `cursor.com`, `downloads.cursor.com` | vendor installer + payload |
| `aider` | `astral.sh` | fetches uv before installing Aider |
| `continue` | `api.continue.dev` | fetches its default config |
| `claude` | `claude.ai`, `downloads.claude.ai` | installer script + release payload, for the self-updating install — see below |

`claude` is the one row that is about **staying current** rather than installing.
It normally runs either way, because the image carries an npm-installed copy —
but that bake is best-effort (`|| true`, so an upstream npm outage cannot break
the image build), and on an image where it did not land there is no fallback left
to reach: the run ends at exit 127 rather than running something older.

The copy that *keeps itself current* is a different one. It lives in the
persisted HOME, installed on first run from `claude.ai/install.sh` and updated
from `downloads.claude.ai` thereafter — neither of which is in the baseline.
Without them the run silently falls back to the image's copy and stays on
whatever version that image was built with. Since the allowlist is now the
*default*, this applies to a plain `sandbox-cli claude`, not only to runs that
pass `--allow`:

```sh
sandbox-cli claude --allow claude.ai --allow downloads.claude.ai
```

Or put them in your own config (`~/.config/sandbox/config.yaml`) so every run has
them:

```yaml
network:
  allow: [claude.ai, downloads.claude.ai]
```

**What you are permitting, stated plainly:** `claude.ai/install.sh` redirects to
`downloads.claude.ai`, which serves both the shell script the container pipes
straight into `bash` and the binary that script then installs — which is why both
names are needed and neither alone is enough. That is the same trust you already
extend by running Claude Code at all — but it is the reason these two are not
simply added to the baseline. Two vendor hosts in the set that *every* run trusts
by default is a different decision from two you typed for a run you were thinking
about, and the config form above is closer to the first: it applies to every run,
in every project, until you remove it.

**You will also need your model provider's API host**, which the baseline only
covers for Anthropic and OpenAI. Add e.g. `generativelanguage.googleapis.com`
(Google), `openrouter.ai`, `api.groq.com`, `dashscope-intl.aliyuncs.com` as
appropriate — and note the allowlist resolves each domain to IPs when the
container starts, so hosts behind rotating CDN addresses can still be refused.

## Browser-callback logins are not supported

Some agents offer a sign-in that finishes by redirecting your browser to a
**loopback address** — `http://127.0.0.1:<port>/callback`. That method **cannot
work in the sandbox**, and it is not a limitation you can configure away:

- The server receiving the callback runs inside the **container**, on the
  container's own `127.0.0.1`. Your host browser follows the redirect to *your*
  loopback and finds nothing listening.
- **`--publish` does not fix it.** A published port forwards to the container's
  *interface* address; a process bound to the container's loopback never sees
  that traffic. The connection is accepted and closed, which looks like an empty
  reply rather than a refusal.
- The redirect URI is usually **registered with the provider** and fixed, so it
  cannot be repointed at something reachable.

Every agent below offers a second method — a device code or a pasted
authorization code — that needs no callback at all. Pick that one:

| Agent | Do **not** pick | Pick this instead |
| --- | --- | --- |
| `codex` | `Sign in with ChatGPT` | `Sign in with Device Code` |
| `opencode` (xAI / SuperGrok) | `xAI Grok OAuth (SuperGrok Subscription)` | `xAI Grok OAuth (Headless / Remote / VPS)` |

The rest of the fleet needs no such choice, and it is worth knowing why rather
than trusting the table. `gemini` and `qwen` **detect** the container and switch
themselves: browser launch is suppressed when Linux has no `DISPLAY`,
`WAYLAND_DISPLAY` or `MIR_SOCKET`, and the sandbox sets none, so they take their
paste-the-code path on their own (`qwen` is additionally forced with
`NO_BROWSER=1`, and `cursor` with `NO_OPEN_BROWSER=1`). `claude` offers a pasted
code and a device code. `copilot`, `droid`, `openhands`, `crush` and `amp` are
device-code or poll-for-result flows to begin with. `cline` has loopback
callbacks but refuses them with an auth message rather than opening a browser —
use `cline auth --provider … --apikey …`, as its section says. `aider` and
`continue` have no login at all.

## Troubleshooting

**"is not installed, and installing it just now failed" (exit 127)**
The agent isn't in the image and the install couldn't run. You have no network,
or you're using `--allow` without the domains above.

**The agent asks me to log in every time.**
You're either passing `--no-persist-auth`, or forwarding a path-valued variable
that moved the agent's state directory (see the list at the top). For Goose and
Droid, check you haven't overridden the keyring switch the sandbox sets.

On **native Linux** this also had a cause of its own, fixed in the version that
carries this note: bind mounts there carry real uids, the container user is uid
1001, and the persisted HOME is a directory you own at mode 0700 — so the agent
could not read the stored credentials, and could not write the ones it had just
obtained either. The login worked and then evaporated with the container. The
container now takes your primary group and the directory is shared with it, so
nothing needs doing. If you are on an older binary, the one-line workaround is
`chmod g+rwx,g+s ~/.config/sandbox/agents/<agent>` plus
`sandbox-cli <agent> --user "1001:$(id -g)"`. macOS was never affected: Docker
Desktop virtualizes bind ownership.

**First run of an agent takes ages.**
Expected — that's the one-time install into the agent home. The table above has
rough sizes. Later runs start immediately.

**Login prints a URL and nothing happens.**
Open the URL on your **host machine**. There is no browser in the container. Most
of these flows are a device code or a poll-for-result, which need no callback at
all.

**The browser opens, then the page cannot connect to `127.0.0.1`.**
You picked a login method that is not supported here — see
[Browser-callback logins are not supported](#browser-callback-logins-are-not-supported)
for which method to pick instead. Publishing the port does not help.

**I want a clean session.**
`--no-persist-auth` runs with a throwaway home; nothing is kept.
