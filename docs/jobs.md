# Jobs, agent runs and secrets

A gateway also runs work after you have gone. A job is a command, or an agent
and a prompt, run in a fresh sandbox per run: the gateway places it, starts
it, waits with a timeout, keeps its output (1 MiB per stream) and the files
you name (8 MiB each), terminates the sandbox, and retries a run that failed.
`prompts` makes a batch, one run per prompt; `parallelism` bounds how many
run at once. A run that does not fit the tenant's quota waits for room.

Jobs and secrets exist only on a gateway ([fleet.md](fleet.md)); a plain
`sandboxd` answers their endpoints `404`. The endpoints are in
[api/v1.md](api/v1.md#jobs-agent-runs-and-secrets).

## A first agent run

```sh
sandbox-cli secret set ANTHROPIC_API_KEY < key.txt      # sealed on the gateway, never shown again
sandbox-cli agent-run claude "fix the failing test" --secret ANTHROPIC_API_KEY --wait
```

`agent-run` is a job of one run: an agent, a prompt, and the same options a
job has but no batch. It prints the job's id; with `--wait` it waits, prints
the run's output and exits with the agent's exit code. `job run --wait` waits
for a whole job and exits 1 unless it succeeded.

An agent runs in its verified headless mode (claude, codex, gemini, opencode,
cline). Your saved login does not come along — the CLI's run copies it in
from your machine, and a job has none — so the agent authenticates with an
API key you keep as a secret: `ANTHROPIC_API_KEY` for claude, `OPENAI_API_KEY`
for codex, `GEMINI_API_KEY` for gemini, the provider's for opencode and cline.
The agent must be in the image or installable from it.

## Jobs and batches

```sh
cat > job.yaml <<'YAML'
agent: claude
prompts: ["review internal/api", "review internal/gateway"]
parallelism: 2
retries: 1
timeout_secs: 1800
secrets: [ANTHROPIC_API_KEY]
keep: {files: [/sandbox/home/review.md]}
notify: https://hooks.example.com/sandbox    # POSTed {job, run, state}; never output
YAML
sandbox-cli job run -f job.yaml
sandbox-cli job ls · job get ID · job output ID 0 [--file PATH] · job cancel ID
```

A job spec is YAML or JSON (`-f -` reads it from stdin):

| Field | |
|---|---|
| `command: [argv…]` | what each run starts. Exactly one of `command` and `agent`. |
| `agent`, `prompt` | an agent in its headless mode, and what to ask it. |
| `prompts: […]` | a batch: one run per prompt, every one to succeed. |
| `parallelism` | how many runs may be in flight at once (default 1). |
| `completions` | how many runs must succeed (default 1; a batch's is its number of prompts). |
| `retries` | how many more attempts a run that failed or timed out gets. |
| `timeout_secs` | bounds each attempt from the moment its command starts (default 3600). |
| `name`, `image` | a name for people (jobs are found by id); the image, else the node's default. |
| `env: {NAME: value}` | set in each run; a reserved name is refused. A `GET` shows names, not values. |
| `secrets: [NAME]` | the tenant's secrets, each set in the run's environment under its own name. |
| `network`, `resources`, `from_snapshot` | as for a sandbox: the egress policy, `{cpus, memory_mb, disk_mb}`, a snapshot to start from. |
| `keep: {output, files}` | what is kept once the sandbox is gone: stdout and stderr (default true, 1 MiB per stream), and the absolute guest paths in `files`, read back when the command ends (8 MiB each). |
| `notify` | a URL told when each run ends and when the job does (below). |

A job is `running` while any run is queued or running, then `succeeded`,
`failed` or `cancelled`. Each run is `queued`, `running`, `succeeded`,
`failed`, `timed_out` or `cancelled`; a failed or timed-out attempt with
retries left goes back to `queued`, and the run's attempts share its index.

`job get ID` shows each run's state, exit code, attempts and sandbox; `job
output ID N` prints what run N kept of stdout (and stderr to stderr), and
`--file PATH` one of its kept files. `job cancel ID` cancels it: runs not
started never are, and running ones' sandboxes are terminated.

A finished job, and what it kept, is kept for a day (`--job-retention`), in
`--jobs-dir` (`jobs/` beside the state file). A restarted gateway picks its
jobs up again: a running command is followed where it runs.

## Secrets

```sh
sandbox-cli secret set ANTHROPIC_API_KEY < key.txt   # the value from stdin
sandbox-cli secret ls                                # names and when each was set; never a value
sandbox-cli secret rm ANTHROPIC_API_KEY
```

A secret goes into a run's environment by name, only for jobs that name it;
the API never returns it. Secrets need the gateway started with
`--secrets-key-file` (32 random bytes, mode 0600), which seals the tenants'
secrets and jobs' environments at rest; without it there are no secrets, and
a job or service that names one is refused with `501`. Setting or removing
one needs a key with the `secrets:write` scope. The name is an environment
variable name that is not reserved; the value is at most 64 KiB, with no NUL.

Secrets are the tenant's: any key of the tenant may name one in a job or a
service, and so read it from inside its sandboxes. A tenant is the unit that
shares secrets, and users with no tenant all share the default one; an
[organisation](organizations.md) is a tenant, with secrets of its own. A
service names them the same way (`secrets: [NAME]`, [services.md](services.md)).

What a secret in a sandbox's environment is protected from, and what it is
not, is in [security/secrets.md](security/secrets.md).

## Notify webhooks

A job's `notify` URL is POSTed `{"event": "run.finished" | "job.finished",
"job", "name"?, "job_state", "run"?, "state"?, "exit_code"?}` when a run ends
and when the job does — never output or a value.

The address rules exist because the gateway makes the request, from inside
the operator's network:

- **`https` to a public address only**, checked when the gateway connects,
  after the name is resolved — so a name that resolves somewhere else by the
  time of the post is still caught, and a user cannot make the gateway call
  its own loopback, the nodes' network or a metadata address.
- **`--notify-allow-private`** lifts this for hook receivers on a private
  network: the gateway then also posts to loopback, private and link-local
  addresses, and over `http` to loopback.

## Who may do what

| | Scope |
|---|---|
| start a job or an agent run, cancel one | `sandbox:create` |
| list jobs, read a job, its output and kept files; list secrets | `sandbox:read` |
| set and remove secrets | `secrets:write` |

Another user's job is not found. A running job whose owner holds no active key
at all in that tenant is cancelled as `DELETE /v1/jobs/{id}` would: its running
sandboxes are terminated, its queued runs never start, and the job and each
run say `cancelled: the owner's access was revoked`. Every run also checks its
owner before it makes a sandbox ([operations.md](operations.md#revoking)).

## From an SDK and Studio

The Python and TypeScript SDKs have `create_job`/`createJob`,
`agent_run`/`agentRun`, `jobs`, `job`, `cancel_job`/`cancelJob`,
`job_output`/`jobOutput`, `job_file`/`jobFile`, `set_secret`/`setSecret`,
`secrets` and `delete_secret`/`deleteSecret`
([sdk/README.md](../sdk/README.md)). In Studio, **Jobs** submits and cancels
jobs and shows each run's kept output and files, and **Secrets** lists names
and sets values in a password field that is never shown again
([studio.md](studio.md)).
