# Secrets: what sandbox-cli protects, and what it does not

A statement of the posture, so you can decide what to hand an agent rather than
infer it from behaviour. The backlog for the parts still open is
[`open-items.md`](open-items.md), item 2; this file is what is true today.

The short version: **sandbox-cli keeps a secret's value out of every place it
would otherwise be written down, and does not keep it away from the agent.**
Those are different promises, and only the first is one this tool can make.

---

## The three ways a secret reaches a container

| how | where the value comes from | reaches the agent as |
|---|---|---|
| `secrets:` in your own config | a host file, a host command, or a host env var, resolved per run | an environment variable |
| `--env NAME` / a wrapper's `EnvAllow` | your shell's environment, forwarded only if set | an environment variable |
| the agent's own login | the persisted HOME at `~/.config/sandbox/agents/<name>` | a file the agent reads |

The third is the one most people forget, and it is usually the most valuable: on
the default auth path it is an **OAuth refresh token**, not an API key. It is
scoped to your whole account, does not expire on its own, and the persisted HOME
is the same directory in *every* project.

---

## What is guaranteed

These are properties of the code, not intentions.

- **A secret value never appears on the docker command line.** `internal/creds`
  resolves references on the host and the values reach the child process
  directly; `runtime.BuildArgs` never renders them. They therefore do not appear
  in `--dry-run` output, in `ps`, or in your shell history.
- **A secret value is never written to a config file by us.** You give a
  *reference* — a path, a command, an env var name — and the resolution happens
  at run time.
- **The audit log records environment variables by name only.**
  `audit.SessionMeta` has nowhere to put a value, deliberately: the broker exists
  to keep secrets out of files, and a log is a file.
- **A project's own `.sandbox.yaml` cannot introduce secrets.** `secrets`, `env`
  and `env_allow` are on the refused list in `config/trust.go` — a repository
  you cloned cannot make your machine resolve a credential. Naming a config with
  `--config <path>` is the deliberate act that overrides this.
- **Under `--profile prod` the persisted HOME is not mounted at all**, and
  `ValidateProfile` refuses a prod config that turns it back on. The refresh
  token is not in the container, so it cannot leak from one.

## What is not guaranteed

Stated as plainly as the guarantees, because a reader who assumes the opposite
will hand over the wrong credential.

- **The agent can read every forwarded secret.** `printenv` is enough. It needs
  the value to authenticate, and nothing between it and the value can hide it
  without terminating TLS — which sandbox-cli has decided **not** to do, because
  the proxy that hid every secret would also hold every secret, every prompt in
  plaintext and a CA private key. So this one is permanent, not pending: the
  answer is to make a leak cheap, which is what the next section is about.
- **A leaked secret can leave over DNS.** The container uses a real resolver, and
  data encoded into query names is not something a connection-level firewall
  sees. Recorded at the bottom of `open-items.md` as knowingly open.
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
account-wide, never-expiring, cross-project credential from the container
entirely, and empties the egress baseline so the permitted set is exactly what
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

A `secrets:` command runs wherever the **API process** runs. Under
`docker compose --profile api` that is a container with neither your `gh` login
nor the tooling the command expects, so brokered secrets fail there with
`exit status 127`. Mounting your credentials into that container to fix it would
hand them to a process already holding the docker socket. Brokered secrets belong
to a host process — which is the deployment that compose file recommends anyway.
