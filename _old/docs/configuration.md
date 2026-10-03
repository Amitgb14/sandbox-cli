# Configuration

Configuration lives in two files, and **the split between them is a security
boundary**. Merged in precedence order (later wins): built-in defaults →
`~/.config/sandbox/config.yaml` → nearest `.sandbox.yaml` → CLI flags. Run
`sandbox-cli config show` to see the effective config, and `sandbox-cli config
path` to see which files were consulted.

- [Your own config](#your-own-config)
- [The project config](#the-project-config)
- [A project config is untrusted](#a-project-config-is-untrusted)

## Your own config

**`~/.config/sandbox/config.yaml`** — only you write it, so it may set anything:

```yaml
image: sandbox-base:0.0.1-9f95ae16   # default; tag is content-addressed
workdir: /workspace
user: sandbox           # non-root; agents refuse --dangerously-skip-permissions as root
# runtime: kata-fc      # stronger isolation (microVM); or runsc for gVisor. default: runc
# engine: podman        # docker (default) or podman
mounts:
  - { host: ~/data, container: /workspace/data, mode: rw }
env:
  NODE_ENV: development
env_allow:            # default-deny: only these host vars are forwarded, if set
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY
security:             # secure-by-default hardening
  no_new_privileges: true     # block setuid privilege escalation
  cap_drop: [ALL]             # drop all Linux capabilities (cap_add: [] to add back)
  pids_limit: 1024            # fork-bomb guard; 0 disables
  memory: ""                  # e.g. 2g — opt-in, empty = unlimited
  cpus: ""                    # e.g. 1.5 — opt-in, empty = unlimited
secrets:              # broker: resolve at run time, forward by name (never on the argv/dry-run)
  GITHUB_TOKEN: { command: gh auth token }   # short-lived token from your own tool
  ANTHROPIC_API_KEY: { file: ~/.secrets/anthropic }
  OPENAI_API_KEY: { env: OPENAI_API_KEY }     # from host env, but kept off the command line
snapshot:             # the crash safety net, and where its snapshots are kept
  retention: 336h             # crash snapshots (14d)
  manual_retention: 168h      # checkpoints you take (7d)
  s3:                         # optional: a copy off this machine, as a git bundle
    bucket: my-sandbox-snapshots
    region: us-east-1
    # endpoint: https://minio.local:9000   # MinIO, R2, Ceph, B2 — empty is AWS
    # path_style: true                     # most self-hosted servers need this
    upload: manual            # manual (default) | all | off
    access_key_env: AWS_ACCESS_KEY_ID      # the variable NAME, never the value
    secret_key_env: AWS_SECRET_ACCESS_KEY
```

`snapshot.s3` holds no credential and has nowhere to put one: `access_key_env`
names an environment variable read when a snapshot is taken. `upload: manual`
mirrors the checkpoints you ask for; `all` adds the crash net, which commits
every two minutes for the length of every run, so it is a real cost per agent
rather than a rounding error.

Retention prunes the **local** copy. Objects in the bucket are left to its own
lifecycle rules — a backup that expires while your laptop is shut is not one.

`sandbox-cli recover fetch` reads what is in there and pulls one back. The
manifest stored beside each bundle is what makes that work on a machine that has
never seen the repository, and the bucket is addressed by an id derived from the
repository's **absolute path** — so a clone in a new location looks in a
namespace of its own, and `--repo-id` reads the one the old path wrote to.

The installer writes this file on a machine that doesn't have one — see
[Install](install.md#the-config-the-installer-writes).

## The project config

**`.sandbox.yaml`** — travels with the repository, so it describes the *project*,
and it may set only the small set that does not touch the boundary:

```yaml
profile: prod         # may only RAISE the profile, never lower it
network:
  mode: allowlist     # may only TIGHTEN: default -> allowlist -> none
  # baseline: false   # dropping the built-in domains tightens, so it is allowed
hostname: devbox
cache:
  enabled: true       # opt in to persisted package caches (npm/pip/cargo/go)
persist_auth: false   # may be turned OFF here (narrows); never on
sync: false           # same
```

`sandbox-cli init` scaffolds one.

The rest of the surface — including the network's `allow:` list, published
`ports:` and the `snapshot:` cadence, all three of which a project file was
permitted to set at first — belongs to you rather than to the repository. Why
each moved is in the next section.

## A project config is untrusted

Anyone who clones the repo runs it, and the agent in the sandbox can rewrite the
file that configures its next run. So a project file is refused any key that
chooses what executes, reaches the host, or relaxes the container — with an error
naming the key.

**Always refused:**

| Key | Because |
|---|---|
| `image`, `runtime`, `engine` | choose what runs, and how strong the boundary is |
| `workdir`, `home` | move the workspace mount and the persisted-auth mount |
| `user` | `user: root` cancels the non-root default |
| `mounts` | the whole host filesystem is one line away |
| `env`, `env_allow` | `PATH`/`LD_PRELOAD`/`NODE_OPTIONS` are code execution; `env_allow` names any host variable to forward |
| `secrets` | `file:` reads any host file you can read, resolved against the repository's own cwd |
| `security.*` | the hardening itself |
| `cache.paths` | a cache path is a writable volume, so it can shadow the credentials directory |
| `network.allow` | it only ever *widens*, and it replaces rather than appends — a checked-in list could become the whole allowlist |
| `ports` | binds a host port, and under an allowlist punches a hole in the default-deny INPUT chain |
| `snapshot` | `enabled: false` removes crash protection; `interval: 1ms` turns the host into a sustained `git add -A` loop; `s3:` names both somewhere to send the working tree and which of your credentials is read to get there |

**Refused only when they weaken** what is already in force — the same key
tightening is exactly what a project file is for: `network.mode`,
`network.baseline`, `profile`, `persist_auth`, `sync`.

Put a refused key in your own config, pass it as a flag, or — if you have read
the file and trust it — load it deliberately with `sandbox-cli --config
./.sandbox.yaml`. Typing the path is the deliberate act discovery never involves.

Discovery is bounded too: it stops at the repository root (or your home
directory), so a stray config in a shared parent like `/tmp` is never picked up.

See also [Security profiles](security/README.md#security-profiles).

---

Next: [Security](security/README.md) · [Common flags](usage/flags.md) ·
[documentation index](README.md)
