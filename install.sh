#!/bin/sh
# Install sandbox-cli: pick the right release archives for this machine and put
# the binaries in the user's home. No root, no package manager.
#
#   curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh
#
# Where sandboxes can run — Linux, and Apple-silicon macOS — this installs the
# client (sandbox-cli), the server (sandboxd) and the guest agent beside it
# (sandbox-guestd). Elsewhere it installs the client, which talks to a sandboxd
# on another machine (sandbox-cli context add).
#
# Options (when run as a file, e.g. `sh install.sh --version 0.1.0`):
#   --version VER   install a specific release        (default: latest)
#   --dest DIR      install directory                 (default: ~/.local/bin)
#   --token TOK     GitHub token for a private repo   (or set GITHUB_TOKEN)
#   --client-only   install sandbox-cli and nothing else
#   --no-config     do not write ~/.config/sandbox/config.yaml
#   --uninstall     remove the binaries, then report what else is left behind
#   --purge         with --uninstall: also delete ~/.config/sandbox (agent
#                   logins!) and sandboxd's state directory (images, volumes,
#                   the audit log)
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
CLIENT_ONLY=0
SERVER="sandboxd"
GUEST="sandbox-guestd"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version)   VERSION="${2:-}"; shift 2 ;;
    --dest)      DEST="${2:-}"; shift 2 ;;
    --token)     TOKEN="${2:-}"; shift 2 ;;
    --no-config) NO_CONFIG=1; shift ;;
    --client-only) CLIENT_ONLY=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge)     PURGE=1; shift ;;
    -h|--help)   sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

# ---- uninstall --------------------------------------------------------------
# Deliberately conservative: the binaries go, everything else is only listed
# unless --purge is given. ~/.config/sandbox holds your agent logins and
# sandboxd's state directory holds your volumes, so deleting either silently
# would lose things nobody asked to lose.
if [ "$UNINSTALL" = 1 ]; then
  cfg="${XDG_CONFIG_HOME:-${HOME}/.config}/sandbox"
  state="${XDG_DATA_HOME:-${HOME}/.local/share}/sandboxd"

  removed=0
  # sandbox-studio-api is beta.15's, removed if a machine still has it.
  for d in "$DEST" "${HOME}/.local/bin" /usr/local/bin; do
    for b in "$BINARY" "$SERVER" "$GUEST" sandbox-studio-api; do
      if [ -f "${d}/${b}" ]; then
        # One that is not ours to remove (root's, in /usr/local/bin) is
        # reported and left: under set -e a failed rm would end the uninstall
        # before --purge ran, and the user would not know how far it got.
        if rm -f "${d}/${b}" 2>/dev/null; then
          info "removed ${d}/${b}"
          removed=1
        else
          info "! could not remove ${d}/${b} (permission); remove it yourself"
        fi
      fi
    done
  done
  if [ "$removed" = 0 ]; then
    info "no ${BINARY} binaries found in ${DEST}, ~/.local/bin or /usr/local/bin"
  fi
  info "a sandboxd still running keeps running until you stop it (launchctl or systemctl)"

  if [ "$PURGE" = 1 ]; then
    for d in "$cfg" "$state"; do
      if [ -d "$d" ]; then
        rm -rf "$d"
        info "removed ${d}"
      fi
    done
    info "purge complete"
  elif [ -d "$cfg" ] || [ -d "$state" ]; then
    info ""
    info "Left in place — re-run with --uninstall --purge to delete these too:"
    [ -d "$cfg" ] && info "  ${cfg}  (config + agent logins)" || true
    [ -d "$state" ] && info "  ${state}  (images, volumes, the audit log)" || true
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

# Where sandboxes can run: Linux (Firecracker, given KVM) and Apple-silicon
# macOS (the native container runtime). An Intel Mac gets the client only.
WITH_SERVER=0
if [ "$CLIENT_ONLY" = 0 ]; then
  case "${OS}/${ARCH}" in
    linux/*|darwin/arm64) WITH_SERVER=1 ;;
  esac
fi

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

# ---- download and verify ------------------------------------------------------
CHECKSUMS=0
if fetch "${BASE}/checksums.txt" "$TMP/checksums.txt" 2>/dev/null; then
  CHECKSUMS=1
else
  info "  ! checksums.txt not published for this release; skipping verification"
fi

verify() { # verify FILE — against the release's checksums.txt
  [ "$CHECKSUMS" = 1 ] || return 0
  name=$(basename "$1")
  expected=$(grep " ${name}\$" "$TMP/checksums.txt" | awk '{print $1}' | head -1)
  if [ -z "$expected" ]; then
    info "  ! ${name} not listed in checksums.txt; skipping verification"
    return 0
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$1" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$1" | awk '{print $1}')
  else
    info "  ! no sha256 tool found; skipping verification"
    return 0
  fi
  [ "$actual" = "$expected" ] || die "checksum mismatch for ${name}
  expected ${expected}
  actual   ${actual}"
  info "  checksum ok: ${name}"
}

info "  downloading ${ARCHIVE}"
fetch "${BASE}/${ARCHIVE}" "$TMP/$ARCHIVE" || die "download failed: ${BASE}/${ARCHIVE}
  If the repository is private, pass --token or set GITHUB_TOKEN."
verify "$TMP/$ARCHIVE"

# ---- check everything, then install ------------------------------------------
# Every binary is extracted and verified before any is installed. Installing the
# client and then finding the server missing left a half-install behind, and on
# a release that predates the microVM rewrite that half was the old,
# container-based client: a working command that did something else.
tar -xzf "$TMP/$ARCHIVE" -C "$TMP" "$BINARY" 2>/dev/null \
  || tar -xzf "$TMP/$ARCHIVE" -C "$TMP" \
  || die "could not extract ${ARCHIVE}"
[ -f "$TMP/$BINARY" ] || die "${BINARY} not found inside ${ARCHIVE}"

# sandboxd comes from the archive already verified; the guest agent from an
# archive of its own (it is a Linux binary even on a Mac, where the VM is
# linux/arm64), verified the same way, and installed beside sandboxd, which is
# where sandboxd looks for it.
if [ "$WITH_SERVER" = 1 ]; then
  tar -xzf "$TMP/$ARCHIVE" -C "$TMP" "$SERVER" 2>/dev/null || true
  if [ ! -f "$TMP/$SERVER" ]; then
    die "${ARCHIVE} has no ${SERVER}, so ${VERSION} predates the microVM rewrite: it is the
  container-based release, and nothing was installed.
  Install a release that has it (--version), or build the current one from source:
    git clone https://github.com/${REPO} && cd sandbox-cli && make build
  --client-only installs this release's client alone."
  fi
  GARCHIVE="${GUEST}_${VERSION}_linux_${ARCH}.tar.gz"
  info "  downloading ${GARCHIVE}"
  fetch "${BASE}/${GARCHIVE}" "$TMP/$GARCHIVE" || die "download failed: ${BASE}/${GARCHIVE}; nothing was installed"
  verify "$TMP/$GARCHIVE"
  tar -xzf "$TMP/$GARCHIVE" -C "$TMP" "$GUEST" 2>/dev/null || die "${GUEST} not found inside ${GARCHIVE}; nothing was installed"
fi

install_bin() { # install_bin NAME — from $TMP to $DEST; stage then rename, so replacing a running binary is atomic
  chmod +x "$TMP/$1"
  mv "$TMP/$1" "$DEST/.$1.new"
  mv "$DEST/.$1.new" "$DEST/$1"
  info "installed ${DEST}/$1"
}

mkdir -p "$DEST"
install_bin "$BINARY"
if [ "$WITH_SERVER" = 1 ]; then
  install_bin "$SERVER"
  install_bin "$GUEST"
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
# may tighten what is below and never loosen it, and the keys that widen what a
# sandbox is handed (image, env, env_allow, secrets, routing, providers) are
# refused from it outright.
#
# What a sandbox can reach is decided by the sandboxd you talk to (its policy);
# this file can only ask for less than that, or name what goes in.

# Security profile. dev = a developer is watching, so a control that cannot be
# satisfied warns; prod = unattended, so it refuses — and prod does not copy
# agent logins into sandboxes at all. A project may raise this, never lower it.
profile: dev

# Egress. Without this block a sandbox gets the server's default policy
# (an allowlist of agent APIs and package registries, on a default sandboxd).
# network:
#   mode: none                      # no network at all
#   mode: allowlist                 # the server's list, plus:
#   allow: [internal.registry.example.com]
#   baseline: false                 # drop the built-in names, so `allow` is all
# Per run: --network none, --allow NAME, --deny NAME.

# The image a sandbox boots. Unset means the server's default.
# image: ghcr.io/you/sandbox-base:1

# Environment for every sandbox: constant values, and host variables forwarded
# by name when set. Values never appear in the audit log; names do.
# env: {GOFLAGS: -mod=mod}
# env_allow: [NPM_TOKEN]

# Secrets resolved on this machine and handed to the sandbox as environment
# variables: from a file, a command, or a host variable.
# secrets:
#   GITHUB_TOKEN: {command: "gh auth token"}

# Agent logins are copied into each sandbox and back out when the run ends.
# persist_auth: false             # never copy them (prod's setting)

# Fall through to another agent when a provider is down, or a run fails having
# changed nothing (sandbox-cli agent claude --fallback codex, per run).
# routing: [claude, codex]
SANDBOX_CONFIG_EOF
}

if [ "$NO_CONFIG" = 1 ]; then
  :
elif [ -f "$CONFIG_FILE" ]; then
  info "kept ${CONFIG_FILE}  (existing config, untouched)"
elif write_default_config 2>/dev/null; then
  info "wrote ${CONFIG_FILE}  (profile: dev; the server's network policy applies)"
else
  # A config is a convenience, not a prerequisite: the built-in defaults are a
  # complete configuration on their own, so a read-only or unwritable home must
  # not fail an install that otherwise worked.
  info "! could not write ${CONFIG_FILE}; continuing with the built-in defaults"
fi

# ---- next steps -------------------------------------------------------------
# sandboxd is installed, not started: how it runs is the machine's business
# (a launch agent, a systemd unit) and each guide says how.
if [ "$WITH_SERVER" = 1 ]; then
  case "$OS" in
    darwin) guide="docs/local-macos.md" ;;
    *)      guide="docs/self-hosting.md  (also needs firecracker and a guest kernel)" ;;
  esac
  info "Start sandboxd: https://github.com/${REPO}/blob/main/${guide}"
else
  info "This machine runs the client only. Point it at a sandboxd:"
  info "  ${BINARY} context add NAME https://HOST:PORT --token-file FILE --ca CA.pem"
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
