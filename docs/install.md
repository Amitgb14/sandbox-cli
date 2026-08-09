# Installing sandbox-cli

Requirements, the one-line install, what the installer writes, every other route,
and how to remove it again.

- [Requirements](#requirements)
- [Install](#install)
- [The config the installer writes](#the-config-the-installer-writes)
- [Other ways to install](#other-ways-to-install)
- [Uninstall](#uninstall)

## Requirements

- Docker — Docker Desktop on macOS/Windows,
  [Docker Engine on Linux](platforms/linux.md). [Podman](platforms/podman.md)
  also works.
- Go 1.25+ only if you build from source

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh
```

That detects your OS and CPU, downloads the matching release archive, verifies it
against the release `checksums.txt`, and installs the binary to
`~/.local/bin/sandbox-cli` — no root, no package manager. It prints a PATH hint if
that directory isn't on your `PATH`.

## The config the installer writes

On a machine that doesn't have one yet it also writes your config,
`~/.config/sandbox/config.yaml`, with every default spelled out and commented so
there's one obvious place to change them:

```yaml
profile: dev            # dev warns when a control can't be satisfied; prod refuses
network:
  mode: default         # the container reaches the internet unrestricted
```

`network.mode: default` is a deliberate relaxation of the built-in dev default
(`allowlist` — default-deny with a baseline of agent APIs and package
registries). Agents differ in which hosts they need, and a fresh install that
has to be taught them first is a fresh install that looks broken. What it costs
is worth knowing: the container still can't touch your host, your other
repositories or your keys — that boundary doesn't depend on the network — but an
agent inside it can post what it *can* read anywhere it likes. One word in that
file bounds it again:

```sh
sandbox-cli config path            # which files were consulted
$EDITOR ~/.config/sandbox/config.yaml
#   mode: allowlist                # default-deny + the baseline + your `allow:` list
#   mode: none                     # no network at all
sandbox-cli run --allow example.com -- npm test   # or ad hoc, for one run
```

Two things the installer will not do: it never touches a `config.yaml` that
already exists (upgrading can't reset what you tightened), and `--no-config`
skips writing one at all. The built-in defaults are a complete configuration on
their own, so the file is a starting point, not a prerequisite.

> **Running unattended?** `--profile prod` requires an allowlist and refuses
> while that `network:` block says `default` — comment the two lines out and prod
> supplies its own default-deny with the baseline off. The refusal is deliberate:
> nobody is watching a prod run, so it won't quietly run wider than it was asked to.

Every key that file accepts is in [Configuration](configuration.md).

## Other ways to install

```sh
# a specific release, or a different directory
sh install.sh --version 0.0.1beta.6 --dest ~/bin

# leave ~/.config/sandbox/config.yaml alone
sh install.sh --no-config

# while the repo is private, authenticate with a token
GITHUB_TOKEN=ghp_... sh install.sh

# Go users
go install github.com/Amitgb14/sandbox-cli/cmd/sandbox-cli@latest

# build from source (needs Go 1.25+)
make install        # go install ./cmd/sandbox-cli
make build          # -> bin/sandbox-cli
```

Only the shell installer writes `~/.config/sandbox/config.yaml`; `go install`
and a build from source give you the binary and nothing else. That's not a
missing step — the built-in defaults are complete, and they're the *stricter*
ones (egress allowlist on). Write the file yourself if you want the same
starting point.

Windows: download the `.zip` from the
[releases page](https://github.com/Amitgb14/sandbox-cli/releases) — the shell
installer covers Linux and macOS only.

Release targets: linux, macOS and Windows on amd64 and arm64.

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --uninstall
```

That removes the `sandbox-cli` binary and then *reports* what else is on disk
without deleting it — because `~/.config/sandbox` holds your agent logins, and
silently deleting it would sign you out of Claude/Codex with no warning. To
remove everything, including those logins, the base image, and the cache volumes:

```sh
sh install.sh --uninstall --purge
```

| What | Where | Removed by |
|---|---|---|
| Binary | `~/.local/bin/sandbox-cli` (also checks `/usr/local/bin`) | `--uninstall` |
| Config + agent logins | `~/.config/sandbox/` | `--purge` |
| Base image | `sandbox-base:*` Docker images | `--purge` |
| Package caches | `sandbox-cache-*` Docker volumes | `--purge` |

Containers are `--rm`, so nothing lingers between runs. Your projects and their
`.sandbox.yaml` files are never touched by either flag.

---

Next: [Linux setup](platforms/linux.md) · [Podman](platforms/podman.md) ·
[the user guide](GUIDE.md) · [documentation index](README.md)
