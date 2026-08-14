#!/bin/sh
# Install sandbox-cli: pick the right release archive for this machine and put
# the binary in the user's home. No root, no package manager.
#
#   curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh
#
# Options (when run as a file, e.g. `sh install.sh --version 0.0.1beta.2`):
#   --version VER   install a specific release        (default: latest)
#   --dest DIR      install directory                 (default: ~/.local/bin)
#   --token TOK     GitHub token for a private repo   (or set GITHUB_TOKEN)
#   --no-config     do not write ~/.config/sandbox/config.yaml
#   --with-studio-api  also install sandbox-studio-api from the same archive
#                   (Studio's control plane; studio.sh passes this)
#   --uninstall     remove the binaries and stop Studio, then report what else
#                   is left behind
#   --purge         with --uninstall: also delete ~/.config/sandbox (agent
#                   logins!), the sandbox and Studio images, and cache volumes
#
# A first install also writes ~/.config/sandbox/config.yaml — the trusted user
# layer — carrying the defaults with `profile: dev` and unrestricted egress, so
# a fresh machine runs any agent without a domain list to maintain. An existing
# file is never touched, so upgrading cannot reset your settings.
#
# POSIX sh; needs curl or wget, plus tar.

set -eu

REPO="Amitgb14/sandbox-cli"
BINARY="sandbox-cli"
VERSION=""
DEST="${HOME}/.local/bin"
TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
UNINSTALL=0
PURGE=0
NO_CONFIG=0
WITH_STUDIO_API=0

# The second binary in the release archive. Not installed by default: it is
# Studio's HTTP control plane, and somebody installing the CLI has not asked for
# a server. studio.sh asks for it.
STUDIO_API="sandbox-studio-api"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version)   VERSION="${2:-}"; shift 2 ;;
    --dest)      DEST="${2:-}"; shift 2 ;;
    --token)     TOKEN="${2:-}"; shift 2 ;;
    --no-config) NO_CONFIG=1; shift ;;
    --with-studio-api) WITH_STUDIO_API=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge)     PURGE=1; shift ;;
    -h|--help)   sed -n '2,23p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

# ---- uninstall --------------------------------------------------------------
# Deliberately conservative: the binary goes, everything else is only listed
# unless --purge is given. ~/.config/sandbox holds your agent logins, so
# deleting it silently would sign you out of Claude/Codex with no warning.
if [ "$UNINSTALL" = 1 ]; then
  cfg="${XDG_CONFIG_HOME:-${HOME}/.config}/sandbox"

  # Docker may be absent or not running; never let that fail the uninstall.
  sandbox_images() {
    command -v docker >/dev/null 2>&1 || return 0
    docker images --filter reference='sandbox-base' -q 2>/dev/null | sort -u
  }
  sandbox_volumes() {
    command -v docker >/dev/null 2>&1 || return 0
    docker volume ls --filter name='sandbox-cache-' -q 2>/dev/null
  }
  studio_images() {
    command -v docker >/dev/null 2>&1 || return 0
    docker images --filter reference='ghcr.io/amitgb14/sandbox-studio-*' -q 2>/dev/null | sort -u
  }

  # Studio is stopped here, and stopping it is not optional the way deleting an
  # image is.
  #
  # It leaves two things *running*: a UI container, and an API process on your
  # host holding the docker socket and a port. Removing the binaries while those
  # stay up is the worst of both — the tool is gone and the server it started is
  # not — so this happens on a plain --uninstall, and only the artifacts on disk
  # wait for --purge. It is also what makes `studio.sh uninstall` unnecessary for
  # anyone who no longer has the script: this installer is the one they already
  # used to get here.
  stop_studio() {
    studio_state="${cfg}/studio"
    if command -v docker >/dev/null 2>&1; then
      for c in sandbox-studio-ui sandbox-studio-api; do
        if docker container inspect "$c" >/dev/null 2>&1; then
          docker rm -f "$c" >/dev/null 2>&1 || true
          info "stopped ${c}"
        fi
      done
    fi
    # The host API, by pid, checked against the process name for the same reason
    # studio.sh checks it: pids are recycled and this file outlives a reboot.
    if [ -r "${studio_state}/api.pid" ]; then
      pid=$(cat "${studio_state}/api.pid" 2>/dev/null || true)
      if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
        case "$(ps -p "$pid" -o comm= 2>/dev/null)" in
          *sandbox-studio-api*) kill "$pid" 2>/dev/null || true; info "stopped the Studio API (pid ${pid})" ;;
        esac
      fi
    fi
  }
  stop_studio

  removed=0
  # Both binaries: an uninstall that left the server behind would leave the one
  # thing here that listens on a port.
  for d in "$DEST" "${HOME}/.local/bin" /usr/local/bin; do
    for b in "$BINARY" "$STUDIO_API"; do
      if [ -f "${d}/${b}" ]; then
        rm -f "${d}/${b}"
        info "removed ${d}/${b}"
        removed=1
      fi
    done
  done
  if [ "$removed" = 0 ]; then
    info "no ${BINARY} binary found in ${DEST}, ~/.local/bin or /usr/local/bin"
  fi

  imgs=$(sandbox_images)
  vols=$(sandbox_volumes)
  simgs=$(studio_images)

  if [ "$PURGE" = 1 ]; then
    if [ -d "$cfg" ]; then
      rm -rf "$cfg"
      info "removed ${cfg}  (config + agent logins)"
    fi
    if [ -n "$imgs" ]; then
      # Unquoted on purpose: one id per line, split into separate arguments.
      docker rmi -f $imgs >/dev/null 2>&1 || true
      info "removed sandbox-base image(s)"
    fi
    if [ -n "$vols" ]; then
      docker volume rm $vols >/dev/null 2>&1 || true
      info "removed sandbox-cache-* volume(s)"
    fi
    if [ -n "$simgs" ]; then
      docker rmi -f $simgs >/dev/null 2>&1 || true
      info "removed sandbox-studio image(s)"
    fi
    info "purge complete"
  else
    # Only print the "left behind" report when something actually is.
    if [ -d "$cfg" ] || [ -n "$imgs" ] || [ -n "$vols" ] || [ -n "$simgs" ]; then
      info ""
      info "Left in place — re-run with --uninstall --purge to delete these too:"
      # `|| true` on each: a failed test is an AND-OR list with status 1, which
      # `set -e` would otherwise treat as fatal and abort the report mid-way.
      [ -d "$cfg" ] && info "  ${cfg}  (config + agent logins)" || true
      [ -n "$imgs" ] && info "  sandbox-base image(s)      docker rmi \$(docker images -q sandbox-base)" || true
      [ -n "$vols" ] && info "  sandbox-cache-* volume(s)  docker volume rm \$(docker volume ls -q -f name=sandbox-cache-)" || true
      [ -n "$simgs" ] && info "  sandbox-studio image(s)    docker rmi \$(docker images -q 'ghcr.io/amitgb14/sandbox-studio-*')" || true
    fi
    info ""
    info "Your projects and their .sandbox.yaml files are never touched."
  fi
  exit 0
fi

# ---- http helper (curl or wget) ---------------------------------------------
# The Accept type is per call, not per script, because the two kinds of URL
# fetched here want different ones and a token makes the difference visible:
# release *assets* need application/octet-stream, and the JSON *API* answers a
# request for that with 415 Unsupported Media Type. Hardcoding octet-stream
# meant every tokened run — the documented way to install from a private repo —
# failed at the version lookup, while untokened runs sent no Accept at all and
# worked. Defaulting to octet-stream keeps the asset call sites unchanged.
if command -v curl >/dev/null 2>&1; then
  fetch() { # fetch URL OUTFILE [ACCEPT]
    if [ -n "$TOKEN" ]; then
      curl -fsSL -H "Authorization: Bearer $TOKEN" \
        -H "Accept: ${3:-application/octet-stream}" -o "$2" "$1"
    else
      curl -fsSL -o "$2" "$1"
    fi
  }
elif command -v wget >/dev/null 2>&1; then
  fetch() {
    if [ -n "$TOKEN" ]; then
      wget -q --header "Authorization: Bearer $TOKEN" \
        --header "Accept: ${3:-application/octet-stream}" -O "$2" "$1"
    else
      wget -q -O "$2" "$1"
    fi
  }
else
  die "need curl or wget"
fi

# ---- detect platform --------------------------------------------------------
os=$(uname -s)
case "$os" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*)
    die "Windows is not supported by this script; download the .zip from
  https://github.com/${REPO}/releases" ;;
  *) die "unsupported operating system: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

# ---- resolve version --------------------------------------------------------
TMP=$(mktemp -d)
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

if [ -z "$VERSION" ]; then
  # The releases list, newest first — not /releases/latest, which silently
  # excludes pre-releases and 404s when every release is one.
  fetch "https://api.github.com/repos/${REPO}/releases?per_page=1" "$TMP/rel.json" \
    "application/vnd.github+json" \
    || die "cannot reach the GitHub API.
  If the repository is private, pass --token or set GITHUB_TOKEN."
  VERSION=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/rel.json" | head -1)
  [ -n "$VERSION" ] || die "no releases found for ${REPO}.
  See https://github.com/${REPO}/releases, or pass --version explicitly."
fi

ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"

info "${BINARY} ${VERSION} -> ${DEST}/${BINARY}"
info "  platform: ${OS}/${ARCH}"

# ---- download ---------------------------------------------------------------
info "  downloading ${ARCHIVE}"
fetch "${BASE}/${ARCHIVE}" "$TMP/$ARCHIVE" || die "download failed: ${BASE}/${ARCHIVE}
  If the repository is private, pass --token or set GITHUB_TOKEN."

# ---- verify checksum --------------------------------------------------------
if fetch "${BASE}/checksums.txt" "$TMP/checksums.txt" 2>/dev/null; then
  expected=$(grep " ${ARCHIVE}\$" "$TMP/checksums.txt" | awk '{print $1}' | head -1)
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
    else
      actual=""
      info "  ! no sha256 tool found; skipping verification"
    fi
    if [ -n "$actual" ]; then
      [ "$actual" = "$expected" ] || die "checksum mismatch for ${ARCHIVE}
  expected ${expected}
  actual   ${actual}"
      info "  checksum ok"
    fi
  else
    info "  ! ${ARCHIVE} not listed in checksums.txt; skipping verification"
  fi
else
  info "  ! checksums.txt not published for this release; skipping verification"
fi

# ---- install ----------------------------------------------------------------
tar -xzf "$TMP/$ARCHIVE" -C "$TMP" "$BINARY" 2>/dev/null \
  || tar -xzf "$TMP/$ARCHIVE" -C "$TMP" \
  || die "could not extract ${ARCHIVE}"
[ -f "$TMP/$BINARY" ] || die "${BINARY} not found inside ${ARCHIVE}"

mkdir -p "$DEST"
chmod +x "$TMP/$BINARY"
# Stage then rename, so replacing a running binary is atomic.
mv "$TMP/$BINARY" "$DEST/.${BINARY}.new"
mv "$DEST/.${BINARY}.new" "$DEST/$BINARY"

info "installed ${DEST}/${BINARY}"

# ---- the studio control plane, on request ------------------------------------
# From the archive already downloaded and already checksummed, so the two halves
# cannot end up at different versions and there is no second thing to verify.
# Releases before this binary existed simply do not carry it, which is a plain
# message rather than a failure: the CLI install above is complete either way.
if [ "$WITH_STUDIO_API" = 1 ]; then
  if [ ! -f "$TMP/$STUDIO_API" ]; then
    tar -xzf "$TMP/$ARCHIVE" -C "$TMP" "$STUDIO_API" 2>/dev/null || true
  fi
  if [ -f "$TMP/$STUDIO_API" ]; then
    chmod +x "$TMP/$STUDIO_API"
    mv "$TMP/$STUDIO_API" "$DEST/.${STUDIO_API}.new"
    mv "$DEST/.${STUDIO_API}.new" "$DEST/$STUDIO_API"
    info "installed ${DEST}/${STUDIO_API}"
  else
    info "! ${VERSION} does not ship ${STUDIO_API}; install a newer release for Studio's API"
  fi
fi

# ---- default user config ----------------------------------------------------
# Written once, on a machine that has none. Two rules make this safe to run from
# a pipe on every upgrade:
#
#   1. An existing file is never touched. This directory also holds your agent
#      logins, and an installer that rewrote your configuration on upgrade would
#      undo whatever you had tightened, silently and at the worst moment.
#   2. The file is the *user* layer, which is the trusted one. Everything it sets
#      you could have typed yourself; nothing here is reachable by a repository.
#
# It carries the defaults with `profile: dev` and `network.mode: default`, so a
# fresh install reaches the whole internet and works with any agent, model
# provider or private registry without a domain list to maintain. That is a
# deliberate relaxation of the built-in dev default (`allowlist`, default-deny
# with a baseline of agent APIs and registries) — one line in the file, written
# where you can see it and edit it, rather than a default you cannot find.
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/sandbox"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"

write_default_config() {
  # Before the mkdir, so the directory is created private too: `secrets:` below
  # resolves credentials, and this is where the agent logins land.
  umask 077
  mkdir -p "$CONFIG_DIR"
  cat > "$CONFIG_FILE" <<'SANDBOX_CONFIG_EOF'
# sandbox-cli — your own configuration.
#
# Written by install.sh on a machine that had none; a later upgrade leaves it
# alone. This is the TRUSTED layer: everything here is something you could type
# on the command line. Precedence, later wins:
#
#   profile base  ->  THIS FILE  ->  a project .sandbox.yaml  ->  flags
#
# A project's .sandbox.yaml travels with the repository, so it is untrusted: it
# may tighten what is below and never loosen it, and the privilege-relevant keys
# (image, user, mounts, secrets, env, security, ...) are refused from it outright.
#
#   sandbox-cli config show    # the fully resolved configuration
#   sandbox-cli config path    # which files were consulted
#   sandbox-cli doctor         # whether this host can deliver it

# Security profile. dev = a developer is watching, so a control that cannot be
# satisfied warns; prod = unattended, so it refuses. A project may raise this to
# prod, never lower it.
profile: dev

# ---------------------------------------------------------------------------
# Egress
# ---------------------------------------------------------------------------
# mode: default  — the container gets an ordinary bridge network and reaches
#                  anything, exactly like any other process on your machine.
#                  This is what the installer writes: agents differ in which
#                  hosts they need, and a list you have to maintain is a list
#                  that eventually blocks the thing you are trying to do.
#
# What you give up by leaving it here: the container still cannot touch your
# host, your other repositories or your keys — that boundary does not depend on
# the network — but a prompt-injected agent inside it can post what it *can*
# read (this project, and any credential you handed it) anywhere it likes.
#
# To bound that, change one word:
#   mode: allowlist   # default-deny, with a baseline of the agent APIs and the
#                     # package registries, plus anything under `allow:` below
#   mode: none        # no network at all
# Ad hoc, for a single run: --allow DOMAIN (implies allowlist), or --network none
#
# NOTE: --profile prod requires an allowlist, so it refuses to run while this
# block says `default` — and a flag cannot lift it, because the profile is
# checked as the configuration resolves. On a machine that runs unattended,
# comment these two lines out (prod then supplies its own default-deny with the
# baseline off) or set `mode: allowlist` here. The refusal is the point of prod:
# nobody is watching, so it will not quietly run wider than it was asked to.
network:
  mode: default
  # allow:                          # extra domains, allowlist mode only
  #   - internal.registry.example.com
  # baseline: false                 # drop the built-in domains so `allow` is
  #                                 # the whole list — the only setting that
  #                                 # also excludes github.com, a write endpoint

# ---------------------------------------------------------------------------
# Everything below is a built-in default, written out so it can be changed.
# Uncomment only what you want to differ.
# ---------------------------------------------------------------------------

# The container image. Unset means the built-in base image, whose tag is a hash
# of its definition, so it rebuilds itself when that changes. Pinning here opts
# out of that.
# image: my-org/my-dev-image:latest

# workdir: /workspace       # where the project is mounted
# user: sandbox             # sandbox (non-root default) | root — agents refuse
#                           # --dangerously-skip-permissions as root
# home: /sandbox/home       # the fake, ephemeral HOME
# hostname: sandbox
# engine: docker            # docker | podman
# runtime: ""               # OCI runtime; "" = the daemon's default (runc).
#                           # runsc (gVisor) or kata-runtime for a stronger
#                           # boundary, if registered with the daemon.

# persist_auth: true        # keep each agent's login in ~/.config/sandbox/agents/<agent>,
#                           # mounted as that agent's whole HOME. --no-persist-auth
#                           # opts out for one run; prod turns it off entirely.
# sync: true                # claude only: mount this project's host history so
#                           # sessions resolve on both sides. --no-sync opts out.

# Container hardening. Pointer fields are tri-state: omit to keep the default.
# security:
#   no_new_privileges: true # block setuid privilege escalation
#   cap_drop: [ALL]         # drop all Linux capabilities (cap_add: [] to add back)
#   pids_limit: 1024        # fork-bomb guard; 0 disables
#   memory: ""              # e.g. 2g — opt-in, empty = unlimited
#   cpus: ""                # e.g. 1.5 — opt-in, empty = unlimited
#   seccomp: ""             # "" = the daemon's default profile
#                           # "required" = refuse to run unless one is applied
#                           # /path/to/profile.json = use that profile

# Crash safety net: the workspace is snapshotted into refs/sandbox while a run
# is in flight, and `sandbox-cli recover` restores it. Your index, HEAD, branches
# and working tree are never written.
# snapshot:
#   enabled: true
#   interval: 2m
#   retention: 336h         # 14 days

# Package-manager caches in named volumes, so they survive the --rm container.
# Opt-in; also available ad hoc via --cache.
# cache:
#   enabled: true
#   paths:                  # added to the built-in npm/pip/cargo/go/yarn set
#     - /sandbox/home/.cache/pnpm

# Ports published to the host. A bare spec binds 127.0.0.1; write 0.0.0.0:3000:3000
# to expose one deliberately.
# ports:
#   - 3000:3000

# Extra mounts beyond the automatic /workspace bind. Host paths may use ~ and may
# be relative to this file. mode defaults to ro. Never /, your home, or an
# ancestor of it — those are refused.
# mounts:
#   - { host: ~/datasets, container: /workspace/data, mode: ro }

# Values injected into every container.
# env:
#   NODE_ENV: development

# Host variables forwarded ONLY if they are set (default-deny allowlist). The
# agent wrappers already forward their own API key this way.
# env_allow:
#   - ANTHROPIC_API_KEY
#   - OPENAI_API_KEY

# Brokered credentials: resolved on the host at run time and passed to the
# container by name, so the value never lands on the docker command line, in
# --dry-run output, or in this file. One source each: file, command, or env.
# secrets:
#   GITHUB_TOKEN:
#     command: gh auth token
SANDBOX_CONFIG_EOF
}

if [ "$NO_CONFIG" = 1 ]; then
  :
elif [ -f "$CONFIG_FILE" ]; then
  info "kept ${CONFIG_FILE}  (existing config, untouched)"
elif write_default_config 2>/dev/null; then
  info "wrote ${CONFIG_FILE}  (profile: dev, unrestricted egress — edit to tighten)"
else
  # A config is a convenience, not a prerequisite: the built-in defaults are a
  # complete configuration on their own, so a read-only or unwritable home must
  # not fail an install that otherwise worked.
  info "! could not write ${CONFIG_FILE}; continuing with the built-in defaults"
fi

# ---- PATH hint --------------------------------------------------------------
case ":${PATH}:" in
  *":${DEST}:"*)
    info "Run: ${BINARY} --help" ;;
  *)
    case "${SHELL:-}" in
      */zsh) rc="~/.zshrc" ;;
      */fish) rc="~/.config/fish/config.fish" ;;
      *) rc="~/.bashrc" ;;
    esac
    printf '\nNote: %s is not on your PATH. Add it:\n' "$DEST"
    printf '  echo '\''export PATH="%s:$PATH"'\'' >> %s && exec $SHELL\n' "$DEST" "$rc" ;;
esac
