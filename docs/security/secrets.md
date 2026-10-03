# Secrets: what sandbox-cli protects, and what it does not

A statement of the posture, so you can decide what to hand an agent rather than
infer it from behaviour. The backlog for the parts still open is
[`open-items.md`](open-items.md), item 2; this file is what is true today.

The short version: **sandbox-cli keeps a secret's value out of every place it
would otherwise be written down, and does not keep it away from the agent.**
Those are different promises, and only the first is one this tool can make.

---

## The three ways a secret reaches a sandbox

| how | where the value comes from | reaches the agent as |
|---|---|---|
| `secrets:` in your own config | a host file, a host command, or a host env var, resolved per run on the machine running the CLI | an environment variable |
| `--env NAME` / an agent's `EnvAllow` | your shell's environment, forwarded only if set | an environment variable |
| the agent's own login | the files saved at `~/.config/sandbox/agents/<name>`, copied in when a run starts | a file the agent reads |

The third is the one most people forget, and it is usually the most valuable: on
the default auth path it is an **OAuth refresh token**, not an API key. It is
scoped to your whole account, does not expire on its own, and the same saved
login is restored into every run of that agent, in every project.

---

## What is guaranteed

These are properties of the code, not intentions.

- **A secret value never appears in an argv, a VM's config or its kernel
  command line.** `internal/creds` resolves references on the host. The values
  travel in the API request body to `sandboxd`, and from there to the guest's
  environment over the agent channel. Neither the Firecracker config nor the
  macOS runtime's argv carries them
  (`TestBuildConfigCarriesNoEnvironmentValue`,
  `TestBuildRunArgsCarryNoEnvironmentValue`), and the API never returns them
  (`EnvironmentValuesAreNeverReturned`, conformance).
- **A secret value is never written to a config file by us.** You give a
  *reference* — a path, a command, an env var name — and the resolution happens
  at run time.
- **The audit log records environment variables by name only, and a process by
  its program, argument count and a hash.** `api.Event` has nowhere to put a
  value or an argument, deliberately: the broker exists to keep secrets out of
  files, a log is a file, and a token pasted into a prompt is an argument.
- **A project's own `.sandbox.yaml` cannot introduce secrets.** `secrets`, `env`
  and `env_allow` are on the refused list in `policy/trust.go` — a repository
  you cloned cannot make your machine resolve a credential. Naming a config with
  `--config <path>` is the deliberate act that overrides this.
- **Under `--profile prod` no saved login is restored or saved**, and
  `ValidateProfile` refuses a prod config that turns it back on. The refresh
  token never enters a sandbox, so it cannot leak from one.
- **A saved login carries the login and nothing else.** Files that are also an
  agent's settings keep only their login keys (`agents.FilterAuth`). An MCP
  server, or another command an agent wrote into them, does not reach later runs.

## What is not guaranteed

Stated as plainly as the guarantees, because a reader who assumes the opposite
will hand over the wrong credential.

- **The agent can read every forwarded secret.** `printenv` is enough. It needs
  the value to authenticate, and nothing between it and the value can hide it
  without terminating TLS — which sandbox-cli has decided **not** to do, because
  the proxy that hid every secret would also hold every secret, every prompt in
  plaintext and a CA private key. So this one is permanent, not pending: the
  answer is to make a leak cheap, which is what the next section is about.
- **A leaked secret can leave through a host you allowed.** If `github.com` is on
  the egress allowlist — as it is under the default baseline — a token can be
  pushed there in a commit message or a gist. The allowlist decides *where*, not
  *what*.
- **Nothing stops a compromised agent from *using* a credential it holds.** This
  survives every mitigation, including the TLS-terminating proxy: there the agent
  does not know the secret and can still act with its full authority.

---

## What to do about it

The threat model is prompt injection, so treat a leak as something to survive
rather than prevent. What decides the damage is **scope, lifetime, breadth and
recoverability** — not how well the value was hidden.

**1. Run `--profile prod` when the credentials matter.** It removes the
account-wide, never-expiring, cross-project credential from every sandbox, and empties the egress baseline so the permitted set is exactly what
you named rather than one that already includes `github.com`.

**2. Broker short-lived, narrowly-scoped secrets.** A `secrets:` entry runs a
command, so this needs nothing new:

```yaml
secrets:
  GITHUB_TOKEN:
    command: gh auth token          # long-lived: leaks for months
```

```yaml
secrets:
  GITHUB_TOKEN:
    command: gh auth token --scope repo   # or an STS/fine-grained mint
```

The agent still reads the value either way. The difference is whether what
leaked is worth anything ten minutes later.

**sandbox-cli says something when it can tell.** A brokered value that looks
long-lived produces one line naming the secret and **what was observed about it**:

```
sandbox-cli: secret GITHUB_TOKEN begins with "gho_" — GitHub OAuth tokens, which
is what `gh auth token` returns. A leaked value stays usable until you revoke it;
brokering a short-lived credential bounds what that is worth. …
```

Two things are checked, and they are not equally good. The **expiry a JWT carries
in its own payload** is a measurement: it works for any issuer, including ones
that do not exist yet, and it cannot go out of date. A **prefix** — `ghp_`,
`glpat-`, `xoxb-` and a few more — is a lookup against a short list, and lists
about the outside world are wrong at the edges forever. That is why the message
reports the prefix it saw rather than announcing whose credential you hold: if a
prefix is later reused by someone else, the sentence stays true and you can
dismiss it.

Read what that does **not** say. It never refuses: for some credentials the
long-lived form is the only form there is, and `ANTHROPIC_API_KEY` has no
ten-minute variant. And **no warning is not a pass** — most credentials are
opaque strings with no lifetime encoded in them, and the prefix list will never
be complete, so silence means nothing was recognized, not that what you brokered
is short-lived. Only you know that.

The check reads the value on the host, reports the format marker and nothing
else of it, and covers `secrets:` only. A credential forwarded with `--env` or a
wrapper's `EnvAllow` is not examined — those are mostly API keys with no
short-lived form, so warning on them would fire on nearly every run and become a
line nobody reads.

**3. Use a different credential per project.** A token shared across projects
reintroduces the breadth that `prod` just removed: one repository's compromise
becomes every repository's.

**4. Keep `--allow` short.** Free under `prod`, since the baseline is already
empty. Fewer reachable hosts is fewer places a leaked secret can be posted.

Together those leave the realistic worst case as *a short-lived token, scoped to
one repository, leaked from one run* — which you rotate, or wait out.

---

## One deployment caveat

A `secrets:` command runs where the **CLI** runs, not where `sandboxd` runs. A
remote or self-hosted `sandboxd` therefore needs nothing of your tooling or
login. The value is resolved on your machine and sent in the create request,
which is one more reason a TCP `sandboxd` needs a token and, off loopback, TLS.
