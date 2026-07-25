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
  [Passing flags to the agent](../README.md#passing-flags-to-the-agent).
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

## Prerequisites shared by all agents

1. **Docker** running (Docker Desktop on macOS/Windows).
2. **`sandbox-cli` installed** — see the [README](../README.md#install).
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

---

# The agents

## claude — Claude Code

- **Prerequisites:** a Claude account (Pro/Max) or `ANTHROPIC_API_KEY`.
- **Setup:** run it and follow the login prompt. Credentials land in the agent
  home and are reused after that.
- **Forwarded if set:** `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
  `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`.
- **Extras unique to this wrapper:** a live memory/CPU status line in Claude's own
  UI (`--no-statusline` to disable), and your **host Claude history for this
  project** is shared by default so a host session can be `--resume`d inside the
  container and vice versa (`--no-sync` to keep them separate).

```sh
sandbox-cli claude
sandbox-cli claude --dangerously-skip-permissions
sandbox-cli claude --worktree feature-a -- -p "implement the API"
```

## codex — Codex CLI

- **Prerequisites:** a ChatGPT account or `OPENAI_API_KEY`.
- **Setup:** `sandbox-cli codex` and log in, or export the key on your host.
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
sandbox-cli opencode
sandbox-cli opencode run 'run the tests'
```

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
sandbox-cli cline task 'run the tests'
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

## Running an agent detached

`--detach` starts the sandbox in the background and prints its container name, so
one terminal can launch several agents (see
[GUIDE.md](GUIDE.md#running-an-agent-in-the-background) for the full cycle). Two
things decide whether an adapter can be used that way:

1. **It needs a non-interactive mode**, because nothing is attached to answer a
   prompt. The documented ones are `claude -p "…"`, `codex exec "…"` and
   `droid exec "…"`; for any other adapter, check its own `--help` before
   detaching it.
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
| `openhands` | `api.github.com` | asks for the latest release tag; falls back to a pinned version without it |
| `continue` | `api.continue.dev` | fetches its default config |

**You will also need your model provider's API host**, which the baseline only
covers for Anthropic and OpenAI. Add e.g. `generativelanguage.googleapis.com`
(Google), `openrouter.ai`, `api.groq.com`, `dashscope-intl.aliyuncs.com` as
appropriate — and note the allowlist resolves each domain to IPs when the
container starts, so hosts behind rotating CDN addresses can still be refused.

## Troubleshooting

**"is not installed, and installing it just now failed" (exit 127)**
The agent isn't in the image and the install couldn't run. You have no network,
or you're using `--allow` without the domains above.

**The agent asks me to log in every time.**
You're either passing `--no-persist-auth`, or forwarding a path-valued variable
that moved the agent's state directory (see the list at the top). For Goose and
Droid, check you haven't overridden the keyring switch the sandbox sets.

**First run of an agent takes ages.**
Expected — that's the one-time install into the agent home. The table above has
rough sizes. Later runs start immediately.

**Login prints a URL and nothing happens.**
Open the URL on your **host machine**. There is no browser in the container. Every
agent here uses either a device code or a poll-for-result flow, so none of them
needs a localhost callback.

**I want a clean session.**
`--no-persist-auth` runs with a throwaway home; nothing is kept.
