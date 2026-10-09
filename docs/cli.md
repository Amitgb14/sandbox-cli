# sandbox-cli command reference

<!-- Generated from the command tree by `make docs-cli` (internal/cli/docs.go). Do not edit by hand. -->

Every command and subcommand of `sandbox-cli`, as `sandbox-cli COMMAND --help` prints it.
Flags go before `--`; everything after it is the sandbox's command. `sandbox-cli completion`
(shell completion scripts) and `sandbox-cli help` are cobra's own and not listed.

Setting up what these commands talk to is in [self-hosting.md](self-hosting.md),
[local-macos.md](local-macos.md) and, for the gateway-only commands (`ssh`, `job`,
`service`, `secret`, `org`, `gateway`), [fleet.md](fleet.md).

Global flags:

| Flag | Default | |
|---|---|---|
| `--org string` |  | on a gateway, the organization to act in (default: SANDBOX_ORG, then the context's, then the key's own tenant) |

## Commands

| Command | |
|---|---|
| [`sandbox-cli agent`](#sandbox-cli-agent) | Run a coding agent in a sandbox, keeping its login between runs |
| [`sandbox-cli agent claude`](#sandbox-cli-agent-claude) | Run claude in a new sandbox |
| [`sandbox-cli agent cline`](#sandbox-cli-agent-cline) | Run cline in a new sandbox |
| [`sandbox-cli agent codex`](#sandbox-cli-agent-codex) | Run codex in a new sandbox |
| [`sandbox-cli agent copilot`](#sandbox-cli-agent-copilot) | Run copilot in a new sandbox |
| [`sandbox-cli agent cursor`](#sandbox-cli-agent-cursor) | Run cursor in a new sandbox |
| [`sandbox-cli agent devin`](#sandbox-cli-agent-devin) | Run devin in a new sandbox |
| [`sandbox-cli agent gemini`](#sandbox-cli-agent-gemini) | Run gemini in a new sandbox |
| [`sandbox-cli agent goose`](#sandbox-cli-agent-goose) | Run goose in a new sandbox |
| [`sandbox-cli agent kilocode`](#sandbox-cli-agent-kilocode) | Run kilocode in a new sandbox |
| [`sandbox-cli agent ls`](#sandbox-cli-agent-ls) | List the agents, whether each can run unattended, and whether a login is saved |
| [`sandbox-cli agent opencode`](#sandbox-cli-agent-opencode) | Run opencode in a new sandbox |
| [`sandbox-cli agent openhands`](#sandbox-cli-agent-openhands) | Run openhands in a new sandbox |
| [`sandbox-cli agent qwen`](#sandbox-cli-agent-qwen) | Run qwen in a new sandbox |
| [`sandbox-cli agent state`](#sandbox-cli-agent-state) | Say what each agent is doing: working, blocked (waiting for you), idle, done or failed |
| [`sandbox-cli agent wait`](#sandbox-cli-agent-wait) | Wait until an agent is in one of the given states (blocked, idle, done, failed, …) |
| [`sandbox-cli agent-run`](#sandbox-cli-agent-run) | Run an agent on a prompt on a gateway's fleet, unattended; prints the job id |
| [`sandbox-cli attach`](#sandbox-cli-attach) | Attach your terminal to a sandbox's process; closing the terminal, or Ctrl-C without one, detaches and leaves it running |
| [`sandbox-cli context`](#sandbox-cli-context) | Choose which sandboxd the CLI talks to: local, self-hosted, or cloud |
| [`sandbox-cli context add`](#sandbox-cli-context-add) | Add a context: https://host:port with --token-file, or unix:///path |
| [`sandbox-cli context ls`](#sandbox-cli-context-ls) | List contexts; * marks the current one |
| [`sandbox-cli context rm`](#sandbox-cli-context-rm) | Remove a context |
| [`sandbox-cli context use`](#sandbox-cli-context-use) | Make NAME the current context |
| [`sandbox-cli doctor`](#sandbox-cli-doctor) | Check the current context's sandboxd and say what it offers |
| [`sandbox-cli events`](#sandbox-cli-events) | Show a sandbox's audit events: what it was asked to do and how it ended |
| [`sandbox-cli exec`](#sandbox-cli-exec) | Run a command in a running sandbox |
| [`sandbox-cli gateway`](#sandbox-cli-gateway) | Operate a running gateway's nodes: list, cordon, drain, lost sandboxes, the audit log (admin key) |
| [`sandbox-cli gateway audit`](#sandbox-cli-gateway-audit) | Show the gateway's audit log: who did what, newest last |
| [`sandbox-cli gateway cordon`](#sandbox-cli-gateway-cordon) | Stop placing new sandboxes on a node; what runs there carries on |
| [`sandbox-cli gateway drain`](#sandbox-cli-gateway-drain) | Cordon a node and report its sandboxes; with --terminate, end them all |
| [`sandbox-cli gateway lost`](#sandbox-cli-gateway-lost) | List sandboxes on nodes that have not answered for longer than the gateway's grace period |
| [`sandbox-cli gateway nodes`](#sandbox-cli-gateway-nodes) | List the gateway's nodes: health, cordon, sandboxes running, sandboxd version |
| [`sandbox-cli gateway uncordon`](#sandbox-cli-gateway-uncordon) | Place new sandboxes on a node again |
| [`sandbox-cli job`](#sandbox-cli-job) | Run commands and agents on a gateway's fleet, after you have gone: one run or a batch |
| [`sandbox-cli job cancel`](#sandbox-cli-job-cancel) | Cancel a job: runs not started never are, and running ones' sandboxes are terminated |
| [`sandbox-cli job get`](#sandbox-cli-job-get) | Show a job and each of its runs |
| [`sandbox-cli job ls`](#sandbox-cli-job-ls) | List your jobs, newest first |
| [`sandbox-cli job output`](#sandbox-cli-job-output) | Print what a run kept of its output (stdout, then stderr to stderr); --file prints a kept file |
| [`sandbox-cli job run`](#sandbox-cli-job-run) | Start a job from a YAML (or JSON) spec; -f - reads it from stdin |
| [`sandbox-cli kill`](#sandbox-cli-kill) | Terminate sandboxes, discarding everything in them |
| [`sandbox-cli list`](#sandbox-cli-list) | List sandboxes |
| [`sandbox-cli logs`](#sandbox-cli-logs) | Print a sandbox process's output from the start, following it until it exits |
| [`sandbox-cli org`](#sandbox-cli-org) | Organizations on a gateway: create one, switch between yours, manage its members |
| [`sandbox-cli org create`](#sandbox-cli-org-create) | Create an organization, with you as its owner (needs the org:create scope) |
| [`sandbox-cli org ls`](#sandbox-cli-org-ls) | List the organizations you may act in; * marks the current one |
| [`sandbox-cli org members`](#sandbox-cli-org-members) | List the current organization's members |
| [`sandbox-cli org members add`](#sandbox-cli-org-members-add) | Add a member to the current organization, or change one's role (owners only) |
| [`sandbox-cli org members rm`](#sandbox-cli-org-members-rm) | Remove a member from the current organization (owners only); what they had open there ends at once |
| [`sandbox-cli org use`](#sandbox-cli-org-use) | Make NAME the organization this context acts in |
| [`sandbox-cli resume`](#sandbox-cli-resume) | Bring a suspended sandbox back as it was |
| [`sandbox-cli run`](#sandbox-cli-run) | Run a command in a new sandbox |
| [`sandbox-cli secret`](#sandbox-cli-secret) | Keep values on a gateway for jobs to name: an agent's API key, a registry token |
| [`sandbox-cli secret ls`](#sandbox-cli-secret-ls) | List your tenant's secrets by name |
| [`sandbox-cli secret rm`](#sandbox-cli-secret-rm) | Remove a secret |
| [`sandbox-cli secret set`](#sandbox-cli-secret-set) | Set a secret, its value read from stdin |
| [`sandbox-cli service`](#sandbox-cli-service) | Services on a gateway: a sandbox spec and a count the gateway keeps running |
| [`sandbox-cli service deploy`](#sandbox-cli-service-deploy) | Create a service, or update it (a rolling update) if it exists |
| [`sandbox-cli service get`](#sandbox-cli-service-get) | Show a service: its spec, its rollout, and each replica's health |
| [`sandbox-cli service ls`](#sandbox-cli-service-ls) | List services |
| [`sandbox-cli service rm`](#sandbox-cli-service-rm) | Delete services and terminate their replicas |
| [`sandbox-cli service scale`](#sandbox-cli-service-scale) | Set how many replicas a service keeps |
| [`sandbox-cli shell`](#sandbox-cli-shell) | Open an interactive shell in a running sandbox |
| [`sandbox-cli snapshot`](#sandbox-cli-snapshot) | Capture a sandbox; start forks of it with run --from-snapshot |
| [`sandbox-cli snapshot-schedule`](#sandbox-cli-snapshot-schedule) | Snapshot a running sandbox on a schedule, keeping the newest few |
| [`sandbox-cli ssh`](#sandbox-cli-ssh) | Open an SSH session to a sandbox through a gateway (or a shell over the API on a plain sandboxd) |
| [`sandbox-cli ssh-access`](#sandbox-cli-ssh-access) | Print a short-lived ssh command for one sandbox, needing no registered key |
| [`sandbox-cli ssh-key`](#sandbox-cli-ssh-key) | Manage the public keys a gateway accepts for SSH logins |
| [`sandbox-cli ssh-key add`](#sandbox-cli-ssh-key-add) | Register a public key (default: ~/.ssh/id_ed25519.pub, id_ecdsa.pub or id_rsa.pub) |
| [`sandbox-cli ssh-key list`](#sandbox-cli-ssh-key-list) | List your registered keys |
| [`sandbox-cli ssh-key rm`](#sandbox-cli-ssh-key-rm) | Remove a registered key |
| [`sandbox-cli studio`](#sandbox-cli-studio) | Open Studio: the browser view of your sandboxes |
| [`sandbox-cli suspend`](#sandbox-cli-suspend) | Stop a sandbox, keeping its memory, processes and disk |
| [`sandbox-cli template`](#sandbox-cli-template) | Sizes a sandbox can be launched at, by name (run --template) |
| [`sandbox-cli template ls`](#sandbox-cli-template-ls) | List the templates: built in, then those saved in Studio |
| [`sandbox-cli tunnel`](#sandbox-cli-tunnel) | Forward a local port to a port inside a sandbox |
| [`sandbox-cli version`](#sandbox-cli-version) | Print the sandbox-cli version |
| [`sandbox-cli volume`](#sandbox-cli-volume) | Named volumes: filesystems that outlive the sandboxes they are mounted in |
| [`sandbox-cli volume create`](#sandbox-cli-volume-create) | Create an empty volume |
| [`sandbox-cli volume ls`](#sandbox-cli-volume-ls) | List volumes and where each is mounted |
| [`sandbox-cli volume rm`](#sandbox-cli-volume-rm) | Delete volumes and everything on them; refused while mounted |
| [`sandbox-cli whoami`](#sandbox-cli-whoami) | Show who the current context's credential is: user, tenant, scopes and key id on a gateway |

## sandbox-cli agent

Run a coding agent in a sandbox, keeping its login between runs.

```text
sandbox-cli agent [command]
```

```text
Runs a coding agent in a new sandbox, in the sandbox user's home, like `run`.
On top of `run`, the agent's login is restored into the sandbox and saved again
when the run ends, and the agent's own environment variables are forwarded when
set — or, where one is not, the API key saved for it in Studio's Agents screen
(~/.config/sandbox/agent-keys.json). The sandbox needs no repository: ask the
agent to clone one.

Leading sandbox flags are consumed; everything after them, or after --, goes
to the agent.
```

Subcommands: [`claude`](#sandbox-cli-agent-claude), [`cline`](#sandbox-cli-agent-cline), [`codex`](#sandbox-cli-agent-codex), [`copilot`](#sandbox-cli-agent-copilot), [`cursor`](#sandbox-cli-agent-cursor), [`devin`](#sandbox-cli-agent-devin), [`gemini`](#sandbox-cli-agent-gemini), [`goose`](#sandbox-cli-agent-goose), [`kilocode`](#sandbox-cli-agent-kilocode), [`ls`](#sandbox-cli-agent-ls), [`opencode`](#sandbox-cli-agent-opencode), [`openhands`](#sandbox-cli-agent-openhands), [`qwen`](#sandbox-cli-agent-qwen), [`state`](#sandbox-cli-agent-state), [`wait`](#sandbox-cli-agent-wait).

Examples:

```sh
sandbox-cli agent claude
sandbox-cli agent claude --network none -- --resume
sandbox-cli agent codex --fallback claude -- exec "clone github.com/you/app and fix its failing test"
```

### sandbox-cli agent claude

Run claude in a new sandbox.

```text
sandbox-cli agent claude [sandbox-flags] [--] [claude-args...]
```

### sandbox-cli agent cline

Run cline in a new sandbox.

```text
sandbox-cli agent cline [sandbox-flags] [--] [cline-args...]
```

### sandbox-cli agent codex

Run codex in a new sandbox.

```text
sandbox-cli agent codex [sandbox-flags] [--] [codex-args...]
```

### sandbox-cli agent copilot

Run copilot in a new sandbox.

```text
sandbox-cli agent copilot [sandbox-flags] [--] [copilot-args...]
```

### sandbox-cli agent cursor

Run cursor in a new sandbox.

```text
sandbox-cli agent cursor [sandbox-flags] [--] [cursor-args...]
```

### sandbox-cli agent devin

Run devin in a new sandbox.

```text
sandbox-cli agent devin [sandbox-flags] [--] [devin-args...]
```

### sandbox-cli agent gemini

Run gemini in a new sandbox.

```text
sandbox-cli agent gemini [sandbox-flags] [--] [gemini-args...]
```

### sandbox-cli agent goose

Run goose in a new sandbox.

```text
sandbox-cli agent goose [sandbox-flags] [--] [goose-args...]
```

### sandbox-cli agent kilocode

Run kilocode in a new sandbox.

```text
sandbox-cli agent kilocode [sandbox-flags] [--] [kilocode-args...]
```

### sandbox-cli agent ls

List the agents, whether each can run unattended, and whether a login is saved.

```text
sandbox-cli agent ls
```

### sandbox-cli agent opencode

Run opencode in a new sandbox.

```text
sandbox-cli agent opencode [sandbox-flags] [--] [opencode-args...]
```

### sandbox-cli agent openhands

Run openhands in a new sandbox.

```text
sandbox-cli agent openhands [sandbox-flags] [--] [openhands-args...]
```

### sandbox-cli agent qwen

Run qwen in a new sandbox.

```text
sandbox-cli agent qwen [sandbox-flags] [--] [qwen-args...]
```

### sandbox-cli agent state

Say what each agent is doing: working, blocked (waiting for you), idle, done or failed.

```text
sandbox-cli agent state [SANDBOX...] [flags]
```

```text
Reports each agent sandbox's state from evidence, never from the agent's wording:
whether its process has exited, who spoke last in its conversation, how long
ago, and whether it has a terminal somebody can answer at. A quiet agent with a
terminal is blocked — waiting for you; without one it is idle. Where the
evidence runs out (no conversation yet, or an agent whose format is not read)
the answer is unknown. --why says what each state means.
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--why` |  | say why each state was reported |

### sandbox-cli agent wait

Wait until an agent is in one of the given states (blocked, idle, done, failed, …).

```text
sandbox-cli agent wait SANDBOX --state STATE [--state STATE...] [flags]
```

```text
Polls the agent's state until it is one of --state, then prints it and exits 0.
A timeout exits 2 and says which state the agent was actually in: it is not a
failure of the agent, and a script that treats it as one will stop work that was
merely slow. A sandbox that is gone ends the wait with an error, unless stopped
was one of the states asked for.
```

Examples:

```sh
sandbox-cli agent wait fix-auth --state blocked --state done --state failed --timeout 30m
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--every duration` | `2s` | how often to look |
| `--state stringArray` |  | a state to wait for (repeatable) |
| `--timeout duration` |  | give up after this long (0: never) |

## sandbox-cli agent-run

Run an agent on a prompt on a gateway's fleet, unattended; prints the job id.

```text
sandbox-cli agent-run AGENT PROMPT [flags]
```

```text
Starts AGENT in its headless mode on PROMPT, in a fresh sandbox the gateway
makes and takes down: a job of one run (sandbox-cli job). Your saved login
does not come along; the agent authenticates with an API key you keep on the
gateway as a secret, named with --secret:

  sandbox-cli secret set ANTHROPIC_API_KEY < key.txt
  sandbox-cli agent-run claude "fix the failing test" --secret ANTHROPIC_API_KEY --wait

The agent must be in the image (or installable from it). With --wait, waits,
prints the run's output and exits with the agent's exit code.

Agents: claude, cline, codex, gemini, opencode.
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `-e, --env stringArray` |  | KEY=VALUE, or KEY to send this machine's value (repeatable) |
| `--image string` |  | image to run (default: the fleet's) |
| `--keep-file stringArray` |  | a guest file to keep when the run ends (absolute path; repeatable) |
| `--name string` |  | name the job |
| `--notify string` |  | a URL POSTed the run's state when it ends (https to a public address, unless the gateway allows private ones) |
| `--retries int` |  | attempts after a failed one |
| `--secret stringArray` |  | a gateway secret to set in the run's environment, by name (repeatable) |
| `--timeout duration` |  | how long the agent may run, e.g. 30m (default: the gateway's, 1h) |
| `--wait` |  | wait, print the output, and exit with the agent's exit code |

## sandbox-cli attach

Attach your terminal to a sandbox's process; closing the terminal, or Ctrl-C without one, detaches and leaves it running.

```text
sandbox-cli attach SANDBOX [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--pid int` |  | process to attach to (default: the running one) |

## sandbox-cli context

Choose which sandboxd the CLI talks to: local, self-hosted, or cloud.

```text
sandbox-cli context [command]
```

Subcommands: [`add`](#sandbox-cli-context-add), [`ls`](#sandbox-cli-context-ls), [`rm`](#sandbox-cli-context-rm), [`use`](#sandbox-cli-context-use).

### sandbox-cli context add

Add a context: https://host:port with --token-file, or unix:///path.

```text
sandbox-cli context add NAME ENDPOINT [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--ca string` |  | CA certificate for a self-hosted endpoint's TLS |
| `--org string` |  | on a gateway, the organization to act in (default: the key's own tenant) |
| `--token-file string` |  | file holding the endpoint's bearer token |

### sandbox-cli context ls

List contexts; * marks the current one.

```text
sandbox-cli context ls
```

### sandbox-cli context rm

Remove a context.

```text
sandbox-cli context rm NAME
```

### sandbox-cli context use

Make NAME the current context.

```text
sandbox-cli context use NAME
```

## sandbox-cli doctor

Check the current context's sandboxd and say what it offers.

```text
sandbox-cli doctor [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli events

Show a sandbox's audit events: what it was asked to do and how it ended.

```text
sandbox-cli events SANDBOX [flags]
```

```text
Prints the events sandboxd recorded for a sandbox: creation with its policy and
labels, every process with its argv and exit code, files read and written,
network changes, and how it ended. Environment variables appear by name only.
A terminated sandbox is still answered by id after the server has forgotten it.
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--json` |  | one JSON event per line |

## sandbox-cli exec

Run a command in a running sandbox.

```text
sandbox-cli exec SANDBOX -- COMMAND [ARGS...] [flags]
```

```text
Runs COMMAND in a running sandbox, on a terminal when yours is one, and exits
with its status. The sandbox keeps running. COMMAND starts in the sandbox
user's home unless --workdir says otherwise.
```

Examples:

```sh
sandbox-cli exec demo -- git clone https://github.com/you/app
sandbox-cli exec demo --workdir /sandbox/home/app -- make test
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `-w, --workdir string` | `/sandbox/home` | directory to run COMMAND in |

## sandbox-cli gateway

Operate a running gateway's nodes: list, cordon, drain, lost sandboxes, the audit log (admin key).

```text
sandbox-cli gateway [command]
```

```text
Calls a gateway's admin API with the current context's key, which needs the
admin scope. Upgrading a node is: drain NODE (or cordon it and wait), upgrade
and restart its sandboxd, uncordon NODE. The gateway keeps a node it cordoned
cordoned across the node's restart, until it is uncordoned here.
```

Subcommands: [`audit`](#sandbox-cli-gateway-audit), [`cordon`](#sandbox-cli-gateway-cordon), [`drain`](#sandbox-cli-gateway-drain), [`lost`](#sandbox-cli-gateway-lost), [`nodes`](#sandbox-cli-gateway-nodes), [`uncordon`](#sandbox-cli-gateway-uncordon).

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli gateway audit

Show the gateway's audit log: who did what, newest last.

```text
sandbox-cli gateway audit [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--limit int` |  | at most this many of the newest entries (default: the gateway's, 100) |
| `--since duration` |  | only entries this recent, e.g. 1h |

### sandbox-cli gateway cordon

Stop placing new sandboxes on a node; what runs there carries on.

```text
sandbox-cli gateway cordon NODE
```

### sandbox-cli gateway drain

Cordon a node and report its sandboxes; with --terminate, end them all.

```text
sandbox-cli gateway drain NODE [flags]
```

```text
Cordons NODE, so it takes no new sandboxes, and says how many it still runs.
Without --terminate they carry on until they end; run it again to see the
count fall. With --terminate every sandbox on the node is terminated now.
```

Flags:

| Flag | Default | |
|---|---|---|
| `--terminate` |  | terminate every sandbox on the node now |

### sandbox-cli gateway lost

List sandboxes on nodes that have not answered for longer than the gateway's grace period.

```text
sandbox-cli gateway lost
```

### sandbox-cli gateway nodes

List the gateway's nodes: health, cordon, sandboxes running, sandboxd version.

```text
sandbox-cli gateway nodes
```

### sandbox-cli gateway uncordon

Place new sandboxes on a node again.

```text
sandbox-cli gateway uncordon NODE
```

## sandbox-cli job

Run commands and agents on a gateway's fleet, after you have gone: one run or a batch.

```text
sandbox-cli job [command]
```

```text
A job is a command (or an agent and a prompt) run in a fresh sandbox per run,
by the gateway, with retries, a timeout and a parallelism limit. Its output
and the files it names are kept for a day after it finishes. Gateway only.
```

Subcommands: [`cancel`](#sandbox-cli-job-cancel), [`get`](#sandbox-cli-job-get), [`ls`](#sandbox-cli-job-ls), [`output`](#sandbox-cli-job-output), [`run`](#sandbox-cli-job-run).

### sandbox-cli job cancel

Cancel a job: runs not started never are, and running ones' sandboxes are terminated.

```text
sandbox-cli job cancel ID [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli job get

Show a job and each of its runs.

```text
sandbox-cli job get ID [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli job ls

List your jobs, newest first.

```text
sandbox-cli job ls [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli job output

Print what a run kept of its output (stdout, then stderr to stderr); --file prints a kept file.

```text
sandbox-cli job output ID RUN [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `--file string` |  | print this kept file (a path from keep.files) instead |

### sandbox-cli job run

Start a job from a YAML (or JSON) spec; -f - reads it from stdin.

```text
sandbox-cli job run -f JOB.yaml [flags]
```

```text
The spec's keys are the API's (docs: README, "Agents and jobs on a fleet"):
name, image, command | agent + prompt, prompts (a batch), parallelism,
completions, retries, timeout_secs, env, secrets, network, resources,
from_snapshot, keep: {output, files}, notify. An unknown key is refused.
```

Examples:

```sh
sandbox-cli job run -f job.yaml
sandbox-cli job run -f job.yaml --wait
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `-f, --file string` |  | the job spec (YAML or JSON); - for stdin |
| `--wait` |  | wait for the job to finish and print it; exit 1 unless it succeeded |

## sandbox-cli kill

Terminate sandboxes, discarding everything in them.

```text
sandbox-cli kill SANDBOX... [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli list

List sandboxes.

```text
sandbox-cli list [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--label stringArray` |  | only sandboxes with this label, key=value (repeatable) |

## sandbox-cli logs

Print a sandbox process's output from the start, following it until it exits.

```text
sandbox-cli logs SANDBOX [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--pid int` |  | process (default: the running one) |

## sandbox-cli org

Organizations on a gateway: create one, switch between yours, manage its members.

```text
sandbox-cli org [command]
```

```text
An organization is a tenant users create and share on a gateway. What is made
in one — sandboxes, volumes, secrets, jobs, services — is its own, with its own
quota, and nobody outside it can see it. A command acts in the organization
--org names, else SANDBOX_ORG, else the context's (org use), else your key's
own tenant, called "default" when it has no name. Gateway only.
```

Subcommands: [`create`](#sandbox-cli-org-create), [`ls`](#sandbox-cli-org-ls), [`members`](#sandbox-cli-org-members), [`use`](#sandbox-cli-org-use).

Examples:

```sh
sandbox-cli org create acme
sandbox-cli org use acme
sandbox-cli org members add bob
sandbox-cli --org acme ls
```

### sandbox-cli org create

Create an organization, with you as its owner (needs the org:create scope).

```text
sandbox-cli org create NAME [flags]
```

```text
NAME is 1 to 30 lowercase letters, digits and dashes, starting with a letter,
with no "--"; "default" and "admin" are reserved, and a name already used by
a tenant is taken.
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli org ls

List the organizations you may act in; * marks the current one.

```text
sandbox-cli org ls [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli org members

List the current organization's members.

```text
sandbox-cli org members [flags]
```

Subcommands: [`add`](#sandbox-cli-org-members-add), [`rm`](#sandbox-cli-org-members-rm).

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli org members add

Add a member to the current organization, or change one's role (owners only).

```text
sandbox-cli org members add USER [flags]
```

```text
USER is named as their API key names them. A user name is unique only within
a tenant, so --tenant says which tenant their keys are in; it defaults to
yours ("default" for keys with none).
```

Examples:

```sh
sandbox-cli org members add bob
sandbox-cli --org acme org members add carol --role owner --tenant team-c
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `--role string` | `member` | owner or member |
| `--tenant string` |  | the tenant of the user's own keys (default: yours) |

### sandbox-cli org members rm

Remove a member from the current organization (owners only); what they had open there ends at once.

```text
sandbox-cli org members rm USER [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `--tenant string` |  | the tenant of the user's own keys (default: yours) |

### sandbox-cli org use

Make NAME the organization this context acts in.

```text
sandbox-cli org use NAME [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

## sandbox-cli resume

Bring a suspended sandbox back as it was.

```text
sandbox-cli resume SANDBOX [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli run

Run a command in a new sandbox.

```text
sandbox-cli run [flags] -- COMMAND [ARGS...]
```

```text
Creates a sandbox, runs COMMAND in the sandbox user's home directory on a
terminal (or streamed, when stdin is not one), and removes the sandbox. A
sandbox needs no repository: clone one inside it if the command wants code.
```

Examples:

```sh
sandbox-cli run -- uname -a
sandbox-cli run -- sh -c 'git clone https://github.com/you/app && cd app && make test'
sandbox-cli run --network none -- make
sandbox-cli run --detach -- ./long-job.sh
```

Flags:

| Flag | Default | |
|---|---|---|
| `--allow stringArray` |  | also allow egress to this host (repeatable; implies allowlist) |
| `--config string` |  | an explicit config file, trusted like your own |
| `--context string` |  | which sandboxd to use (sandbox-cli context ls) |
| `--cpus float64` |  | vCPUs |
| `--deny stringArray` |  | refuse egress to this host even if allowed (repeatable) |
| `-d, --detach` |  | start, print how to attach, and return |
| `--disk int` |  | writable disk in MiB |
| `-e, --env stringArray` |  | KEY=VALUE, or KEY to forward the host's value (repeatable) |
| `--fallback stringArray` |  | an agent to try next if this one's provider is down (repeatable; agent wrappers only) |
| `--from-snapshot string` |  | start from a snapshot (sandbox-cli snapshot) instead of the image |
| `--idle int` |  | terminate after this many idle seconds (default: the server's) |
| `--image string` |  | image to run (default: the server's) |
| `--keep` |  | keep the sandbox when the command ends |
| `--label stringArray` |  | label the sandbox, key=value (repeatable); shown by list and recorded in its audit events |
| `--memory int` |  | memory in MiB |
| `--name string` |  | name the sandbox |
| `--network string` |  | none, allowlist or open (default: the server's) |
| `--no-persist-auth` |  | do not restore or save the agent's login |
| `--profile string` |  | dev or prod (prod: no persisted logins) |
| `--snapshot-every duration` |  | snapshot the sandbox this often while it runs, e.g. 30m (the server sets the shortest allowed) |
| `--snapshot-keep int` |  | how many scheduled snapshots to keep, newest first (default 1) |
| `--template string` |  | size the sandbox from a template: micro, small, medium, large, xlarge or one saved in Studio (--cpus, --memory, --disk override it) |
| `--volume stringArray` |  | mount a named volume, NAME:/path or NAME:/path:ro (repeatable; sandbox-cli volume) |

## sandbox-cli secret

Keep values on a gateway for jobs to name: an agent's API key, a registry token.

```text
sandbox-cli secret [command]
```

```text
A secret is a value your tenant keeps on the gateway, sealed at rest, that a
job sets in its runs' environment by name (secrets: [NAME], or agent-run
--secret NAME). The API never returns a value once it is set. Gateway only.
```

Subcommands: [`ls`](#sandbox-cli-secret-ls), [`rm`](#sandbox-cli-secret-rm), [`set`](#sandbox-cli-secret-set).

### sandbox-cli secret ls

List your tenant's secrets by name.

```text
sandbox-cli secret ls [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli secret rm

Remove a secret.

```text
sandbox-cli secret rm NAME [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli secret set

Set a secret, its value read from stdin.

```text
sandbox-cli secret set NAME [flags]
```

Examples:

```sh
sandbox-cli secret set ANTHROPIC_API_KEY < key.txt
printf %s "$TOKEN" | sandbox-cli secret set REGISTRY_TOKEN
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

## sandbox-cli service

Services on a gateway: a sandbox spec and a count the gateway keeps running.

```text
sandbox-cli service [command]
```

```text
A service is a sandbox spec and a number of replicas a sandbox-gateway keeps
true: it replaces a replica that fails its health check or is lost with its
node, spreads replicas across nodes, rolls a change out one replica at a
time, and routes HTTP to the healthy ones when the service is public.
Services need a gateway; a single sandboxd has none. See docs/services.md.
```

Subcommands: [`deploy`](#sandbox-cli-service-deploy), [`get`](#sandbox-cli-service-get), [`ls`](#sandbox-cli-service-ls), [`rm`](#sandbox-cli-service-rm), [`scale`](#sandbox-cli-service-scale).

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which gateway to use |

### sandbox-cli service deploy

Create a service, or update it (a rolling update) if it exists.

```text
sandbox-cli service deploy -f service.yaml [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `-f, --file string` |  | the service's YAML spec (- for stdin); required |

### sandbox-cli service get

Show a service: its spec, its rollout, and each replica's health.

```text
sandbox-cli service get NAME
```

### sandbox-cli service ls

List services.

```text
sandbox-cli service ls
```

### sandbox-cli service rm

Delete services and terminate their replicas.

```text
sandbox-cli service rm NAME...
```

### sandbox-cli service scale

Set how many replicas a service keeps.

```text
sandbox-cli service scale NAME REPLICAS
```

## sandbox-cli shell

Open an interactive shell in a running sandbox.

```text
sandbox-cli shell SANDBOX [flags]
```

```text
Starts bash (or sh, where the image has no bash) in a running sandbox, in the
sandbox user's home, and connects your terminal to it. Exiting the shell leaves
the sandbox running. SANDBOX is an id or a name, as `sandbox-cli list` shows.
```

Examples:

```sh
sandbox-cli shell demo
sandbox-cli shell sbx_0123456789abcdef --context box
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli snapshot

Capture a sandbox; start forks of it with run --from-snapshot.

```text
sandbox-cli snapshot SANDBOX [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli snapshot-schedule

Snapshot a running sandbox on a schedule, keeping the newest few.

```text
sandbox-cli snapshot-schedule SANDBOX [flags]
```

Examples:

```sh
sandbox-cli snapshot-schedule demo --every 30m --keep 3
sandbox-cli snapshot-schedule demo --off
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |
| `--every duration` |  | how often, e.g. 30m (the server sets the shortest allowed) |
| `--keep int` |  | how many to keep, newest first (default 1) |
| `--off` |  | stop the schedule; snapshots already taken stay |

## sandbox-cli ssh

Open an SSH session to a sandbox through a gateway (or a shell over the API on a plain sandboxd).

```text
sandbox-cli ssh [flags] SANDBOX [-- COMMAND [ARGS...]]
```

```text
Against a gateway, registers your public key if it is not already, pins the
gateway's SSH host key in ~/.config/sandbox/known_hosts, and runs your
ssh client: `ssh -p PORT SANDBOX@HOST`. Afterwards plain ssh works too. With
COMMAND, runs it instead of a login shell. The exit status is ssh's.
Flags go before SANDBOX: everything after it is the command's.

The key is --identity (a .pub file, or a private key with its .pub beside
it), else the first of ~/.ssh/id_ed25519.pub, id_ecdsa.pub, id_rsa.pub.
Only the public half is read; ssh does the authentication.

A gateway lets you in only while you hold an active API key with the
sandbox:ssh scope, and closes an open session once you no longer do.

A plain sandboxd has no SSH server: there this opens a shell (or runs
COMMAND) in the sandbox through the API instead, and says so.
```

Examples:

```sh
sandbox-cli ssh demo
sandbox-cli ssh demo -- uname -a
sandbox-cli ssh --identity ~/.ssh/work_ed25519 --context fleet sbx_n1_0123456789abcdef
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `-i, --identity string` |  | public key file to log in with (or its private half) |

## sandbox-cli ssh-access

Print a short-lived ssh command for one sandbox, needing no registered key.

```text
sandbox-cli ssh-access SANDBOX [flags]
```

```text
Asks the gateway for a token that logs in to SANDBOX until it expires, and
prints the ssh command that uses it. The token is the SSH user name and the
whole credential: anyone holding the line can log in until it expires, so
hand it only to whoever should have that access. Gateway only.
```

Examples:

```sh
sandbox-cli ssh-access demo
sandbox-cli ssh-access demo --ttl 5m
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `--ttl duration` |  | how long the token is valid, e.g. 15m (default: the gateway's) |

## sandbox-cli ssh-key

Manage the public keys a gateway accepts for SSH logins.

```text
sandbox-cli ssh-key [command]
```

```text
A gateway's SSH server logs you in with a public key you registered:
`ssh SANDBOX@gateway -p PORT`. These commands add, list and remove yours.
A login needs an active API key with the sandbox:ssh scope, and removing
a key closes the connections made with it.
A plain sandboxd has no SSH server and refuses them.
```

Subcommands: [`add`](#sandbox-cli-ssh-key-add), [`list`](#sandbox-cli-ssh-key-list), [`rm`](#sandbox-cli-ssh-key-rm).

### sandbox-cli ssh-key add

Register a public key (default: ~/.ssh/id_ed25519.pub, id_ecdsa.pub or id_rsa.pub).

```text
sandbox-cli ssh-key add [FILE] [flags]
```

Examples:

```sh
sandbox-cli ssh-key add
sandbox-cli ssh-key add ~/.ssh/work_ed25519.pub --sandbox demo
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |
| `--sandbox string` |  | accept the key for this sandbox only (default: all of yours) |

### sandbox-cli ssh-key list

List your registered keys.

```text
sandbox-cli ssh-key list [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

### sandbox-cli ssh-key rm

Remove a registered key.

```text
sandbox-cli ssh-key rm ID [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

## sandbox-cli studio

Open Studio: the browser view of your sandboxes.

```text
sandbox-cli studio [flags]
```

```text
Serves Studio on a loopback port and prints the address to open, which carries a
token made for this launch: anyone else on this machine can reach the port, and
the token is what keeps them out. Studio talks to the sandboxd of the current
context (or --context) and holds its token itself; the browser never sees it.

Ctrl-C stops Studio; sandboxes it started keep running.
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd Studio talks to |
| `--port int` | `7080` | loopback port to serve on (0: any free one) |
| `--ui-dir string` |  | serve the UI from this directory instead of the one built in (studio/out, when working on it) |

## sandbox-cli suspend

Stop a sandbox, keeping its memory, processes and disk.

```text
sandbox-cli suspend SANDBOX [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli template

Sizes a sandbox can be launched at, by name (run --template).

```text
sandbox-cli template [command]
```

```text
A template is a size — vCPUs, memory and disk — by name: micro, small, medium,
large and xlarge are built in, and Studio's Templates screen saves more, in
~/.config/sandbox/studio.json. Launch at one with `sandbox-cli run --template
NAME` or `sandbox-cli agent <name> --template NAME`; --cpus, --memory and --disk
given beside it win for their own field. sandboxd's limits still apply.
```

Subcommands: [`ls`](#sandbox-cli-template-ls).

### sandbox-cli template ls

List the templates: built in, then those saved in Studio.

```text
sandbox-cli template ls
```

## sandbox-cli tunnel

Forward a local port to a port inside a sandbox.

```text
sandbox-cli tunnel SANDBOX [LOCAL:]PORT [flags]
```

Examples:

```sh
sandbox-cli tunnel sbx_… 3000          # localhost:3000 -> the sandbox's 3000
sandbox-cli tunnel sbx_… 8080:3000
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

## sandbox-cli version

Print the sandbox-cli version.

```text
sandbox-cli version
```

## sandbox-cli volume

Named volumes: filesystems that outlive the sandboxes they are mounted in.

```text
sandbox-cli volume [command]
```

```text
A volume keeps what a sandbox wrote for the next one that mounts it: a package
cache, a dataset, a model's weights. Mount one with
`sandbox-cli run --volume NAME:/path[:ro]`. A volume is mounted in one live
sandbox at a time, never in a system directory.
```

Subcommands: [`create`](#sandbox-cli-volume-create), [`ls`](#sandbox-cli-volume-ls), [`rm`](#sandbox-cli-volume-rm).

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which sandboxd to use |

### sandbox-cli volume create

Create an empty volume.

```text
sandbox-cli volume create NAME [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--size int` |  | size in MiB (default: the server's default disk size) |

### sandbox-cli volume ls

List volumes and where each is mounted.

```text
sandbox-cli volume ls
```

### sandbox-cli volume rm

Delete volumes and everything on them; refused while mounted.

```text
sandbox-cli volume rm NAME...
```

## sandbox-cli whoami

Show who the current context's credential is: user, tenant, scopes and key id on a gateway.

```text
sandbox-cli whoami [flags]
```

Flags:

| Flag | Default | |
|---|---|---|
| `--context string` |  | which endpoint to use |

