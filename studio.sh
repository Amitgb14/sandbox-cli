#!/bin/sh
# Sandbox Studio, in one command: the UI from a published image, the control
# plane on your host, and the CLI they both drive.
#
#   curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh
#
# Run it from the repository you want to work in. Re-running it is a restart.
#
# Commands (when run as a file, e.g. `sh studio.sh status`):
#   up        install what is missing, pull, start, print the URL   (default)
#   down      stop the UI container and the API process
#   status    what is running, which repository it manages, whether it answers
#   logs      follow the API log (`logs ui` for the UI container)
#   uninstall stop everything, remove the containers, images and Studio's state
#
# Options:
#   --project DIR    repository to manage        (default: this git repo)
#   --config PATH    sandbox config to trust     (default: normal discovery)
#   --port N         UI port                     (default: 3100)
#   --api-port N     API port                    (default: 8787)
#   --tag TAG        image tag to pull           (default: latest)
#   --version VER    release for the binaries    (default: latest)
#   --dest DIR       where binaries go           (default: ~/.local/bin)
#   --token TOK      bearer token to use         (default: generated once)
#   --api-in-docker  run the API as a container too — read the warning below
#   --api-only       run the daemon and agents only; prints the URL and token
#                    for another machine's Studio  (a remote Linux box)
#   --ui-only        run the browser half only, against a daemon elsewhere
#   --api-url URL    which daemon the UI talks to  (implies --ui-only)
#   --bind ADDR      address the daemon listens on  (default: 127.0.0.1)
#   --allow-host H   extra Host the daemon answers to   (repeatable)
#   --cors-origin U  extra origin it accepts, with scheme  (repeatable)
#   --no-install     use the binaries already on this machine
#   --no-pull        do not refresh the image
#
# ── Two machines: agents there, browser here ─────────────────────────────────
#
# The daemon and the containers can live on another machine while the browser
# stays in front of you. "Remote" has to mean the *whole* of sandbox-cli is
# remote: every safety refusal is evaluated against the filesystem it runs on, so
# a local daemon pointed at a remote docker would validate paths here and mount
# paths there.
#
#   # on the Linux box
#   sh studio.sh up --api-only
#
#   # it prints a URL and a token; on your laptop
#   sh studio.sh up --api-url http://10.0.0.5:8787
#
# or set the same two values in the UI at Settings → Connection, which is what
# lets one Studio reach several boxes, and which can save more than one.
#
# Two things bite when the daemon is on another machine, in this order:
#
#   1. the port is closed. A server distribution denies inbound by default —
#      `firewall-cmd --add-port=8787/tcp` or `ufw allow ... 8787`. Check it with
#      `curl http://<box>:8787/v1/health`, which needs no token.
#   2. the origin. `--port` here is the port the *browser's* Studio runs on: the
#      daemon builds its CORS origins from it. Mismatch it and the network works
#      while every request is refused on the origin check, which looks exactly
#      like the daemon being down.
#
# There is no TLS here, so three shapes hold and they are not equal:
#
#   1. a tunnel — `ssh -N -L 8787:127.0.0.1:8787 you@box`, then
#      `--api-url http://localhost:8787`. The daemon stays on loopback, so every
#      check in guard.go stays true and the transport is SSH's. Nothing new to
#      trust, and the recommended shape.
#   2. a reverse proxy terminating TLS in front (Caddy, nginx), which is what to
#      use when several people reach the machine:
#
#        sh studio.sh up --api-only --allow-host api.example.com \
#          --cors-origin https://studio.example.com
#
#      Both flags *add* to what this script works out for itself, because a
#      proxied deployment's names are not derivable from --bind or --port: the
#      browser dials one name and the page is served from another. The daemon
#      stays on 127.0.0.1, so the proxy is the only way in. On your own machine,
#      `--ui-only --api-url https://api.example.com`. See docs/studio-api/README.md.
#   3. `--bind` on a private network you already trust, knowing the token and
#      every prompt cross it in cleartext. The daemon refuses a routable address
#      with no token at all.
#
# ── The repository it starts in, and the ones you add ────────────────────────
#
# `-project` is the repository the API is *started* in, and it stays fixed for
# the life of the process: it is what every request naming no repository is
# about, and it is the one that cannot be removed. Standing in another
# repository changes nothing on its own, which is confusing precisely because
# the terminal moved and the browser did not.
#
# Other repositories are added from the UI — "Add repository…" in the sidebar's
# picker, or the ＋ beside the repository field on Launch — which posts the host
# path to `POST /v1/projects`. The daemon checks it (absolute, on disk, a git
# repository, not your home directory) and remembers it in
# ~/.config/sandbox/studio/projects.json, so it is still there next time.
# Removing one forgets it; nothing on disk is touched.
#
# Two limits worth knowing:
#
#   * The default repository is changed by restarting, not from the UI:
#
#       cd ~/other-project && sh studio.sh up      # or: up --project DIR
#
#     That stops the pair and starts it again against the new repository,
#     keeping the same ports and the same token, so the tab you already have
#     open follows. `status` prints whichever repository the daemon reports.
#
#   * --api-in-docker stays single-repository. The API container is started with
#     only `-v "$PROJECT:$PROJECT"`, so a repository added later is a path it
#     cannot see whatever the registry says. The default (the API as an ordinary
#     host process) has no such limit — it runs as you, and reaches what you can.
#
# ── What runs where, and why it is split ─────────────────────────────────────
#
# The UI is a web app. It holds nothing, reaches nothing, and every screen in it
# is a view of what the API answers — so it ships as an image you pull, and it
# is published for exactly that reason.
#
# The API launches containers. In a container that means mounting the host's
# docker socket into it, and a process holding that socket can start a container
# mounting `/` — root on the host, which is the blast radius sandbox-cli exists
# to prevent. A host process already has the access it needs and hands it to
# nobody. So the API runs on your host by default; `--api-in-docker` is there
# because people will want it anyway, and it is better read than guessed.
#
# ── --config, and why it is not automatic ────────────────────────────────────
#
# A project's `.sandbox.yaml` travels with the repository, so discovery refuses
# the privilege-relevant keys in it — image, mounts, secrets, env, env_allow, a
# weakening network.mode — and the API will not start rather than honour them
# quietly. Naming the path is the deliberate act that makes that file trusted,
# which is exactly the escape hatch the refusal describes. This script forwards
# the flag and never guesses it: reading the file is your part.
#
# POSIX sh; needs docker, plus curl or wget.

set -eu

REPO="Amitgb14/sandbox-cli"
REGISTRY="ghcr.io/amitgb14"

UI_IMAGE="sandbox-studio-ui"
API_IMAGE="sandbox-studio-api"
UI_NAME="sandbox-studio-ui"
API_NAME="sandbox-studio-api"
API_BIN_NAME="sandbox-studio-api"

CMD=""
PROJECT=""
CONFIG=""
UI_PORT=3100
API_PORT=8787
UI_PORT_SET=0
API_PORT_SET=0
TAG=""
VERSION=""
DEST="${HOME}/.local/bin"
TOKEN="${SANDBOX_STUDIO_TOKEN:-}"
API_IN_DOCKER=0
# The two halves, separable. --api-only runs the daemon and its agents (a remote
# Linux box); --ui-only runs the browser half against a daemon somebody else is
# running (your laptop). Neither is the default: one machine running both is
# still the common case.
API_ONLY=0
UI_ONLY=0
API_URL=""
# --bind is the address the daemon listens on. Loopback unless said otherwise,
# because binding a routable port is a decision rather than a default — and the
# daemon itself now refuses one without a token.
BIND="127.0.0.1"
NO_INSTALL=0
NO_PULL=0
# Extra names the daemon should answer to, and extra origins it should accept —
# both **added** to what this script works out for itself, never replacing it.
#
# That direction is the same one the daemon takes with -allow-host (loopback is
# always allowed and the flag adds to it) and the same one a fleet task takes
# with `allow`. It is what a proxied deployment needs: the browser dials
# api.example.com and the page is served from https://studio.example.com, and
# neither is derivable from --bind or --port. Space-separated because this is
# POSIX sh and there are no arrays.
EXTRA_HOSTS=""
EXTRA_ORIGINS=""

STATE="${XDG_CONFIG_HOME:-${HOME}/.config}/sandbox/studio"
PIDFILE="${STATE}/api.pid"
LOGFILE="${STATE}/api.log"
TOKENFILE="${STATE}/token"
PORTFILE="${STATE}/ports"

die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*"; }
warn() { printf '! %s\n' "$*" >&2; }
have() { command -v "$1" >/dev/null 2>&1; }

# The value of a flag that must have one.
#
# An empty value used to travel: `--dest "$BIN"` with BIN unset became DEST="",
# and the failure surfaced two screens later as "not found in  or on PATH" with
# a blank where a directory belongs. That is a shell mistake being reported as a
# missing binary. A flag given nothing is refused where it is typed.
need() { # need FLAG VALUE
  [ -n "${2:-}" ] || die "$1 needs a value"
  printf '%s' "$2"
}

while [ $# -gt 0 ]; do
  case "$1" in
    up|down|status|logs|uninstall) CMD="$1"; shift ;;
    ui|api)          ARG="$1"; shift ;;   # `logs ui` / `logs api`
    --project)       PROJECT=$(need --project "${2:-}"); shift 2 ;;
    --config)        CONFIG=$(need --config "${2:-}"); shift 2 ;;
    --port)          UI_PORT=$(need --port "${2:-}"); UI_PORT_SET=1; shift 2 ;;
    --api-port)      API_PORT=$(need --api-port "${2:-}"); API_PORT_SET=1; shift 2 ;;
    --tag)           TAG=$(need --tag "${2:-}"); shift 2 ;;
    --version)       VERSION=$(need --version "${2:-}"); shift 2 ;;
    --dest)          DEST=$(need --dest "${2:-}"); shift 2 ;;
    --token)         TOKEN=$(need --token "${2:-}"); shift 2 ;;
    --api-in-docker) API_IN_DOCKER=1; shift ;;
    --api-only)      API_ONLY=1; shift ;;
    --ui-only)       UI_ONLY=1; shift ;;
    --api-url)       API_URL=$(need --api-url "${2:-}"); UI_ONLY=1; shift 2 ;;
    --bind)          BIND=$(need --bind "${2:-}"); shift 2 ;;
    --allow-host)    EXTRA_HOSTS="$EXTRA_HOSTS $(need --allow-host "${2:-}")"; shift 2 ;;
    --cors-origin)   EXTRA_ORIGINS="$EXTRA_ORIGINS $(need --cors-origin "${2:-}")"; shift 2 ;;
    --no-install)    NO_INSTALL=1; shift ;;
    --no-pull)       NO_PULL=1; shift ;;
    # The header comment, however long it has grown: a fixed line range silently
    # truncated --help mid-sentence every time a section was added.
    -h|--help)       sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown argument: $1  (try --help)" ;;
  esac
done
CMD="${CMD:-up}"
ARG="${ARG:-}"

# `up` records the ports it chose; every other command reads them back, unless
# this invocation named its own. Without it, `status` after
# `up --api-port 9000` probed 8787 and reported a healthy Studio as absent —
# the ports were a fact about the running pair that nothing had written down.
if [ -r "$PORTFILE" ]; then
  saved_ui=$(sed -n '1p' "$PORTFILE" 2>/dev/null || true)
  saved_api=$(sed -n '2p' "$PORTFILE" 2>/dev/null || true)
  if [ "$UI_PORT_SET" = 0 ] && [ -n "${saved_ui:-}" ]; then UI_PORT="$saved_ui"; fi
  if [ "$API_PORT_SET" = 0 ] && [ -n "${saved_api:-}" ]; then API_PORT="$saved_api"; fi
fi

# The binaries move together, so one version means one flag rather than two that
# can disagree. An explicit --tag still wins.
[ -n "$TAG" ] || TAG="${VERSION:-latest}"

UI_REF="${REGISTRY}/${UI_IMAGE}:${TAG}"
API_REF="${REGISTRY}/${API_IMAGE}:${TAG}"

# ---- shared helpers ----------------------------------------------------------

if have curl; then
  http_ok() { curl -fsS -o /dev/null --max-time 2 "$1" 2>/dev/null; }
  http_get() { curl -fsS --max-time 2 "$1" 2>/dev/null; }
  fetch_stdout() { curl -fsSL "$1"; }
elif have wget; then
  http_ok() { wget -q -O /dev/null --timeout=2 "$1" 2>/dev/null; }
  http_get() { wget -q -O- --timeout=2 "$1" 2>/dev/null; }
  fetch_stdout() { wget -qO- "$1"; }
else
  die "need curl or wget"
fi

require_docker() {
  have docker || die "docker is not installed.
  Docker Desktop: https://docs.docker.com/get-started/get-docker/"
  docker info >/dev/null 2>&1 || die "the docker daemon is not reachable — start Docker and try again."
}

# The repository root, not the directory you happen to be standing in.
#
# Studio addresses everything by branch, and a worktree belongs to a repository,
# so a subdirectory is never the right answer: handed one, the API answers every
# branch-addressed request with "not a git repository" — the exact failure the
# compose file's SANDBOX_PROJECT exists to avoid.
resolve_project() {
  [ -n "$PROJECT" ] || PROJECT=$(pwd)
  [ -d "$PROJECT" ] || die "no such directory: ${PROJECT}"
  root=$(git -C "$PROJECT" rev-parse --show-toplevel 2>/dev/null || true)
  if [ -n "$root" ]; then
    if [ "$root" != "$PROJECT" ]; then
      info "  project: ${root}  (repository root of ${PROJECT})"
    fi
    PROJECT="$root"
  else
    warn "${PROJECT} is not a git repository — Studio's worktrees, diffs and
  branch-addressed runs will not work. Run this from a repository, or pass
  --project."
  fi
}

# One token, generated once and kept, so a restart does not invalidate the copy
# already in your browser. 0600 in a 0700 directory: it is a credential for a
# server that can start containers.
resolve_token() {
  # Exported rather than interpolated at each use: `docker run -e NAME` with no
  # value takes it from this process's environment, so the token stays out of
  # the argv — which is visible in `ps` to every other user on this machine, and
  # afterwards in `docker inspect`. The host path has always done this via the
  # -token flag's env default; the container paths were passing `-e NAME=value`
  # and undoing it.
  if [ -n "$TOKEN" ]; then export SANDBOX_STUDIO_TOKEN="$TOKEN"; return 0; fi
  if [ -r "$TOKENFILE" ]; then
    TOKEN=$(cat "$TOKENFILE")
    if [ -n "$TOKEN" ]; then export SANDBOX_STUDIO_TOKEN="$TOKEN"; return 0; fi
  fi
  if have openssl; then
    TOKEN=$(openssl rand -hex 32)
  else
    TOKEN=$(dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
  fi
  [ -n "$TOKEN" ] || die "could not generate a token; pass --token"
  (umask 077; mkdir -p "$STATE"; printf '%s\n' "$TOKEN" > "$TOKENFILE")
  export SANDBOX_STUDIO_TOKEN="$TOKEN"
}

# Alive *and* still the process we started.
#
# The pidfile lives in ~/.config/sandbox and outlives a reboot, while pids are
# recycled — so `kill -0` alone eventually says yes about somebody else's
# process, and `down` (which `up` calls first) would signal it. The command name
# is the cheap half of the answer; it is not proof, but it turns "some process
# has this number" into "a sandbox-studio-api has this number".
api_running() {
  [ -f "$PIDFILE" ] || return 1
  pid=$(cat "$PIDFILE" 2>/dev/null || true)
  [ -n "$pid" ] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  # The full argv rather than `comm`, because `ps -o comm=` **truncates to 15
  # characters on Linux** — "sandbox-studio-" — so a comparison against the whole
  # name matched on macOS, where comm is the full path, and never on Linux. The
  # daemon there was live and answering while `status` called it stopped and
  # `down` refused to stop the process it had started itself.
  #
  # `args` falls back to `comm` for a kernel that hides an argv this process may
  # not read; a truncated name is still evidence, and this check is deliberately
  # evidence rather than proof (see the note above).
  proc=$(ps -p "$pid" -o args= 2>/dev/null || true)
  [ -n "$proc" ] || proc=$(ps -p "$pid" -o comm= 2>/dev/null || true)
  case "$proc" in
    *sandbox-studio-api*|*sandbox-studio-) return 0 ;;
    *) return 1 ;;
  esac
}

container_exists() { docker container inspect "$1" >/dev/null 2>&1; }

# Whole seconds: a fractional `sleep` is a GNU/BSD extension, and this script
# should not be the reason it fails on a smaller shell.
# What this run wrote, and nothing earlier.
api_log_this_run() {
  if [ "$API_IN_DOCKER" = 1 ]; then
    docker logs --tail 20 "$API_NAME" 2>&1
    return
  fi
  offset=${API_LOG_OFFSET:-0}
  # +1 because tail counts from the byte after the offset.
  tail -c "+$((offset + 1))" "$LOGFILE" 2>/dev/null | head -n 20
}

# wait_for_api URL — like wait_for, but gives up the moment the daemon is gone.
#
# A daemon that refuses its config exits in milliseconds, and waiting twenty
# seconds for a process that is not there says "did not answer" when the truth is
# "would not start". They send a reader to different places: one to the network,
# the other to the file it was told about.
wait_for_api() { # wait_for_api URL PID
  i=0
  while [ "$i" -lt 20 ]; do
    if http_ok "$1"; then return 0; fi
    if [ -n "${2:-}" ] && ! kill -0 "$2" 2>/dev/null; then return 2; fi
    i=$((i + 1))
    sleep 1
  done
  return 1
}

wait_for() { # wait_for URL
  i=0
  while [ "$i" -lt 20 ]; do
    if http_ok "$1"; then return 0; fi
    i=$((i + 1))
    sleep 1
  done
  return 1
}

# ---- down --------------------------------------------------------------------

do_down() {
  stopped=0
  for c in "$UI_NAME" "$API_NAME"; do
    if container_exists "$c"; then
      docker rm -f "$c" >/dev/null 2>&1 || true
      info "stopped ${c}"
      stopped=1
    fi
  done
  if api_running; then
    pid=$(cat "$PIDFILE")
    kill "$pid" 2>/dev/null || true
    # Waited for rather than assumed: `up` calls this and then binds the same
    # port, and a signal only asks. Without the wait a restart raced its own
    # predecessor for the socket and died with "address already in use" —
    # reported as "Studio did not come up", on the path the docs call a restart.
    i=0
    while [ "$i" -lt 10 ] && kill -0 "$pid" 2>/dev/null; do
      i=$((i + 1))
      sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
      sleep 1
    fi
    info "stopped the API process"
    stopped=1
  fi
  rm -f "$PIDFILE"
  [ "$stopped" = 1 ] || info "nothing was running"
}

# ---- status ------------------------------------------------------------------

do_status() {
  info "Studio"
  if container_exists "$UI_NAME"; then
    state=$(docker container inspect -f '{{.State.Status}}' "$UI_NAME" 2>/dev/null || echo unknown)
    image=$(docker container inspect -f '{{.Config.Image}}' "$UI_NAME" 2>/dev/null || echo "?")
    info "  ui    ${state}  ${image}"
  else
    info "  ui    not running"
  fi

  if container_exists "$API_NAME"; then
    state=$(docker container inspect -f '{{.State.Status}}' "$API_NAME" 2>/dev/null || echo unknown)
    info "  api   ${state}  (container)"
  elif api_running; then
    info "  api   running  (host process, pid $(cat "$PIDFILE"))"
  else
    info "  api   not running"
  fi

  # The repository, asked of the running daemon rather than inferred from the
  # directory this command was typed in — those are exactly the two things that
  # drift apart, and the confusion is that the terminal moves and Studio does
  # not. Parsed with sed because jq is not a prerequisite for this script.
  health=$(http_get "http://127.0.0.1:${API_PORT}/v1/health" || true)
  if [ -n "$health" ]; then
    info "  http://127.0.0.1:${API_PORT}/v1/health answers"
    proj=$(printf '%s' "$health" | sed -n 's/.*"project":"\([^"]*\)".*/\1/p')
    if [ -n "$proj" ]; then
      info "  repository  ${proj}"
      here=$(git rev-parse --show-toplevel 2>/dev/null || true)
      if [ -n "$here" ] && [ "$here" != "$proj" ]; then
        info "              (you are in ${here} — run \`up\` here to point Studio at it)"
      fi
    fi
  else
    info "  nothing answers on http://127.0.0.1:${API_PORT}/v1/health"
  fi
  info "  ui at http://localhost:${UI_PORT}"
}

# ---- uninstall ---------------------------------------------------------------

# Everything this script created, and nothing it did not.
#
# It exists because the alternative was fetching the script again to get rid of
# it. Scoped deliberately: the containers, the images it pulled, and its own
# state directory — while the binaries and ~/.config/sandbox (agent logins,
# worktrees, the audit log) belong to sandbox-cli and are named rather than
# deleted. install.sh --uninstall is where those live, and it says the same about
# what it leaves behind.
do_uninstall() {
  do_down

  # Every tag of the two repositories this script pulls, not just the one this
  # invocation happens to name. `uninstall` after `up --tag edge` would otherwise
  # leave the edge images behind, which is not what the word means.
  #
  # Matched on the fully qualified name, so an unrelated local image called
  # `sandbox-studio-ui:latest` — built by hand, from another registry, or from a
  # checkout — is somebody else's and stays.
  for repo in "${REGISTRY}/${UI_IMAGE}" "${REGISTRY}/${API_IMAGE}"; do
    for ref in $(docker images --filter "reference=${repo}" --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | sort -u); do
      docker rmi -f "$ref" >/dev/null 2>&1 || true
      info "removed image ${ref}"
    done
  done

  if [ -d "$STATE" ]; then
    rm -rf "$STATE"
    info "removed ${STATE}  (token, ports, api log)"
  fi

  info ""
  info "Left in place, deliberately:"
  info "  the binaries        sh install.sh --uninstall"
  info "  ~/.config/sandbox   agent logins, worktrees, the run audit log"
  info "                      (install.sh --uninstall --purge removes those too)"
}

# ---- logs --------------------------------------------------------------------

do_logs() {
  case "$ARG" in
    ui)
      container_exists "$UI_NAME" || die "the UI container is not running"
      exec docker logs -f "$UI_NAME" ;;
    *)
      if container_exists "$API_NAME"; then exec docker logs -f "$API_NAME"; fi
      [ -f "$LOGFILE" ] || die "no API log at ${LOGFILE} — is it running?"
      exec tail -f "$LOGFILE" ;;
  esac
}

# ---- up ----------------------------------------------------------------------

install_binaries() {
  [ "$NO_INSTALL" = 0 ] || return 0

  # Already installed and no version asked for: nothing to do.
  #
  # install.sh resolves "latest" against api.github.com, which is 60 requests an
  # hour unauthenticated and needs the network at all — so running it on every
  # `up` made a *restart* of an already-installed Studio fail on a rate-limited
  # or offline machine, with `set -e` turning that into no Studio rather than a
  # missed upgrade. Pass --version to force the download.
  if [ -z "$VERSION" ] && [ -x "${DEST}/sandbox-cli" ] &&
     { [ "$API_IN_DOCKER" = 1 ] || [ -x "${DEST}/${API_BIN_NAME}" ]; }; then
    return 0
  fi

  set -- --dest "$DEST"
  if [ -n "$VERSION" ]; then set -- "$@" --version "$VERSION"; fi
  # The API binary comes from the same archive as the CLI, so both halves are one
  # download and cannot end up at different versions. Not needed when the API is
  # going to be a container.
  if [ "$API_IN_DOCKER" = 0 ]; then set -- "$@" --with-studio-api; fi

  # A checkout is the copy people iterate on, so prefer the installer sitting
  # next to this file — but only when this file is genuinely a file. Piped from
  # curl, `$0` is the shell's own name and `dirname` yields `.`, which would run
  # an `install.sh` that merely happened to be in whatever directory the user was
  # standing in. That is somebody else's script, executed because of where they
  # were.
  here=""
  case "$0" in
    */studio.sh|studio.sh) if [ -f "$0" ]; then here=$(dirname -- "$0"); fi ;;
  esac
  if [ -n "$here" ] && [ -f "${here}/install.sh" ]; then
    sh "${here}/install.sh" "$@"
  else
    fetch_stdout "https://raw.githubusercontent.com/${REPO}/main/install.sh" > "${TMP}/install.sh" \
      || die "could not download install.sh"
    sh "${TMP}/install.sh" "$@"
  fi
}

resolve_api_bin() {
  for c in "${DEST}/${API_BIN_NAME}" "$(command -v "$API_BIN_NAME" 2>/dev/null || true)"; do
    if [ -n "$c" ] && [ -x "$c" ]; then API_BIN="$c"; return 0; fi
  done
  die "${API_BIN_NAME} not found in ${DEST} or on PATH.
  Drop --no-install to fetch it, or use --api-in-docker."
}

# reachable_hosts prints the names this machine can be dialled by, one per line:
# its hostname first, then every non-loopback IPv4 it holds.
#
# It exists because a wildcard bind says what the daemon listens *on* and nothing
# about what a browser will type, while guard.go's Host check compares against
# exactly that. Best effort by design — an address this cannot discover is one
# the operator names with --bind explicitly, and the guard's refusal says which
# host it rejected.
reachable_hosts() {
  hostname 2>/dev/null
  if command -v ipconfig >/dev/null 2>&1; then          # macOS
    for i in $(ipconfig getiflist 2>/dev/null); do
      ipconfig getifaddr "$i" 2>/dev/null
    done
  elif command -v ip >/dev/null 2>&1; then              # Linux
    ip -4 -o addr show scope global 2>/dev/null | awk '{split($4,a,"/"); print a[1]}'
  fi
  # Always successful: an interface with no address makes the last command fail,
  # and under  that would abort a launch over a question this only
  # answers as a courtesy.
  return 0
}

start_api_host() {
  resolve_api_bin
  mkdir -p "$STATE"
  # Through the environment rather than -token: an argument is visible in the
  # process table to every other user on this machine, and the flag already
  # defaults to this variable.
  # -config is always passed; empty means "discover normally", which is what the
  # flag's own default is, so there is no second code path for the common case.
  # SANDBOX_STUDIO_TOKEN is exported by resolve_token, and -token defaults to it.
  # -allow-host names the address a browser dials, which for a remote daemon is
  # not a loopback name — guard.go refuses anything else, and says so. Passed
  # only when binding off loopback, so the local case keeps exactly the surface
  # it had.
  # A wildcard bind is not an address anybody dials, so it is not one the guard
  # can be given: the Host header carries the name the browser typed
  # (10.0.0.5:8787), and -allow-host 0.0.0.0 matches none of them — every remote
  # request was refused by the check that exists to protect it. So the wildcard
  # case allows this machine's own reachable names instead, and the loopback case
  # keeps exactly the surface it had.
  allow_hosts=""
  case "$BIND" in
    127.0.0.1|localhost|::1) ;;
    0.0.0.0|::|"*")          allow_hosts=$(reachable_hosts) ;;
    *)                       allow_hosts="$BIND" ;;
  esac
  allow_args=""
  for h in $allow_hosts $EXTRA_HOSTS; do
    allow_args="$allow_args -allow-host $h"
  done
  # The origins this script can work out are the local UI's. A proxied
  # deployment's page is served from a name only the operator knows, so it is
  # added here rather than derived — and it has to carry its scheme, because a
  # browser sends `https://studio.example.com` and an origin allowlist compares
  # the whole string.
  origin_args=""
  for o in $EXTRA_ORIGINS; do
    origin_args="$origin_args -cors-origin $o"
  done

  # Where this run's lines begin. The log is appended to across restarts, so a
  # plain `tail` on failure shows six previous startups and buries the one
  # sentence that matters — which is exactly how a config refusal read as a
  # mystery timeout.
  API_LOG_OFFSET=$(wc -c < "$LOGFILE" 2>/dev/null || echo 0)

  # shellcheck disable=SC2086  # allow_args and origin_args are deliberately word-split lists
  nohup "$API_BIN" \
    -addr "${BIND}:${API_PORT}" \
    -project "$PROJECT" \
    -config "$CONFIG" \
    $allow_args \
    $origin_args \
    -cors-origin "http://localhost:${UI_PORT}" \
    -cors-origin "http://127.0.0.1:${UI_PORT}" \
    >>"$LOGFILE" 2>&1 &
  echo $! > "$PIDFILE"
}

start_api_container() {
  # The same two lists the host path builds. Repeated rather than shared because
  # the two launches differ in everything else — one is a process, one is a
  # container published on a port — but a deployment that works one way and not
  # the other is exactly the kind of difference nobody finds until they switch.
  #
  c_allow_args=""
  for h in $EXTRA_HOSTS; do
    c_allow_args="$c_allow_args -allow-host $h"
  done
  c_origin_args=""
  for o in $EXTRA_ORIGINS; do
    c_origin_args="$c_origin_args -cors-origin $o"
  done

  warn "the API container mounts /var/run/docker.sock, which is root on this host:
  anything that can reach it can start a container mounting /. That is the
  boundary sandbox-cli exists to hold. Drop --api-in-docker to run it as an
  ordinary host process instead."
  [ "$NO_PULL" = 1 ] || docker pull -q "$API_REF" >/dev/null
  # shellcheck disable=SC2086  # c_allow_args and c_origin_args are deliberately word-split
  docker run -d \
    --name "$API_NAME" \
    -p "127.0.0.1:${API_PORT}:8787" \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "${PROJECT}:${PROJECT}" \
    ${CONFIG:+-v "${CONFIG}:${CONFIG}:ro"} \
    -v "${HOME}/.config/sandbox:${HOME}/.config/sandbox" \
    -e "HOME=${HOME}" \
    -e SANDBOX_STUDIO_TOKEN \
    -e GIT_CONFIG_COUNT=1 \
    -e GIT_CONFIG_KEY_0=safe.directory \
    -e GIT_CONFIG_VALUE_0='*' \
    "$API_REF" \
    -addr 0.0.0.0:8787 \
    -project "$PROJECT" \
    -config "$CONFIG" \
    $c_allow_args \
    $c_origin_args \
    -cors-origin "http://localhost:${UI_PORT}" \
    -cors-origin "http://127.0.0.1:${UI_PORT}" >/dev/null
}

start_ui() {
  [ "$NO_PULL" = 1 ] || { info "  pulling ${UI_REF}"; docker pull -q "$UI_REF" >/dev/null; }
  # Published to loopback: this UI can drive a daemon that starts containers, so
  # it is for this machine. The API URL and the token are handed over at run
  # time — the image bakes neither, because the port and the token are not
  # knowable when it is built.
  docker run -d \
    --name "$UI_NAME" \
    -p "127.0.0.1:${UI_PORT}:3100" \
    -e "SANDBOX_API_URL=${API_URL:-http://localhost:${API_PORT}}" \
    -e SANDBOX_STUDIO_TOKEN \
    "$UI_REF" >/dev/null
}

# do_up_api_only runs the half that owns the containers: sandbox-cli, the daemon,
# and a token. For a Linux box you drive from somewhere else.
#
# It prints the two values the other machine's Settings screen asks for, because
# that is the whole handover — and prints the token *once*, here, rather than
# leaving somebody to find it in a file.
do_up_api_only() {
  resolve_project
  resolve_token
  # Re-running is a restart here too. do_down tolerates a half that was never
  # started, which is exactly this machine's shape.
  do_down >/dev/null 2>&1 || true
  install_binaries
  (umask 077; mkdir -p "$STATE"; printf '%s\n%s\n' "$UI_PORT" "$API_PORT" > "$PORTFILE")

  info "starting the daemon only (no UI on this machine)"
  start_api_host

  # Probed where it actually listens. A --bind daemon is not on 127.0.0.1, so a
  # hardcoded loopback check reported "did not come up" for a daemon that had
  # come up perfectly — on exactly the flag combination this mode exists for.
  api_pid=$(cat "$PIDFILE" 2>/dev/null || true)
  api_status=0
  wait_for_api "http://${BIND}:${API_PORT}/v1/health" "$api_pid" || api_status=$?
  if [ "$api_status" = 0 ]; then
    info "  api  ${BIND}:${API_PORT}"
  else
    if [ "$api_status" = 2 ]; then
      warn "the daemon exited without answering — it refused to start, rather than being slow"
    fi
    api_log_this_run | sed 's/^/    /' || true
    die "the daemon did not come up. The lines above are its own account of why."
  fi

  info ""
  # The address to type, which a wildcard bind is not.
  advertise="$BIND"
  case "$BIND" in
    0.0.0.0|::|"*") advertise=$(reachable_hosts | sed -n 2p) ;;
  esac
  [ -n "$advertise" ] || advertise="$BIND"

  info "On the machine with the browser, open Studio → Settings → Connection:"
  info "  Daemon URL   http://${advertise}:${API_PORT}"
  info "  Token        ${TOKEN}"
  info ""
  if [ -n "$EXTRA_HOSTS" ] && [ "$BIND" = "127.0.0.1" ]; then
    # A name was given, so a proxy is in front and the tunnel advice below is
    # about a URL nobody will dial. Printing it anyway is how an operator who
    # chose one shape is talked back into another.
    for h in $EXTRA_HOSTS; do
      info "Behind your proxy, the Daemon URL is https://${h} — this process stays"
      info "on 127.0.0.1:${API_PORT}, so the proxy is the only way in."
      break
    done
    info ""
    info "The UI half has to run somewhere too. On this machine, beside the daemon:"
    info "  sh studio.sh up --ui-only --api-url https://${h}"
    info "which publishes it on 127.0.0.1:${UI_PORT} for the proxy to serve."
  elif [ "$BIND" = "127.0.0.1" ]; then
    info "This daemon is on loopback, which is the safe default — reach it with a tunnel:"
    info "  ssh -N -L ${API_PORT}:127.0.0.1:${API_PORT} $(id -un)@$(hostname)"
    info "then use http://localhost:${API_PORT} as the Daemon URL."
  else
    warn "bound to ${BIND}, and there is no TLS here: the token and everything it
  protects cross the network in cleartext. Prefer --bind 127.0.0.1 with an SSH
  tunnel unless this is a private network you trust."
  fi
}

# do_up_ui_only runs the browser half against a daemon somebody else is running.
# No binaries, no pidfile, no local port to wait on — there is nothing here but a
# container and a URL.
do_up_ui_only() {
  require_docker
  # These configure a daemon, and this path starts none. Silence would leave the
  # operator watching every request refused on the origin check with nothing to
  # connect it to — the failure mode the flags exist to prevent, arriving through
  # the flags themselves.
  if [ -n "$EXTRA_HOSTS" ] || [ -n "$EXTRA_ORIGINS" ]; then
    warn "--allow-host and --cors-origin configure the daemon, and this starts only the
  UI. Pass them on the machine running the daemon (sh studio.sh up --api-only …)."
  fi
  target="${API_URL:-http://localhost:${API_PORT}}"
  docker rm -f "$UI_NAME" >/dev/null 2>&1 || true
  [ "$NO_PULL" = 1 ] || { info "  pulling ${UI_REF}"; docker pull -q "$UI_REF" >/dev/null; }
  (umask 077; mkdir -p "$STATE"; printf '%s\n%s\n' "$UI_PORT" "$API_PORT" > "$PORTFILE")

  API_URL="$target" start_ui
  info "Studio UI  http://localhost:${UI_PORT}"
  info "  daemon   ${target}"
  info ""
  info "The URL and token can also be set in the UI: Settings → Connection. A value"
  info "typed there outranks this one, which is what lets one UI reach several boxes."
}

do_up() {
  if [ "$UI_ONLY" = 1 ]; then do_up_ui_only; return; fi
  if [ "$API_ONLY" = 1 ]; then do_up_api_only; return; fi

  require_docker
  resolve_project
  resolve_token

  # Re-running is a restart. Leaving the old pair up would mean a port clash on
  # one half and two servers disagreeing about the project on the other.
  do_down >/dev/null 2>&1 || true

  install_binaries

  # Written down before anything binds them, so `status`, `logs` and `down`
  # address the pair that is actually running rather than the defaults.
  (umask 077; mkdir -p "$STATE"; printf '%s\n%s\n' "$UI_PORT" "$API_PORT" > "$PORTFILE")

  info "starting Studio"
  if [ "$API_IN_DOCKER" = 1 ]; then
    start_api_container
  else
    start_api_host
  fi

  api_pid=$(cat "$PIDFILE" 2>/dev/null || true)
  # Captured with `|| status=$?` rather than read from `$?` after the call: this
  # script runs under `set -e`, so a bare command returning non-zero ends it
  # there and then — silently, with the daemon's status as the script's, which
  # is precisely the mystery this block exists to remove.
  api_status=0
  wait_for_api "http://127.0.0.1:${API_PORT}/v1/health" "$api_pid" || api_status=$?
  case "$api_status" in
    0) info "  api  http://127.0.0.1:${API_PORT}" ;;
    2)
      warn "the daemon exited without answering — it refused to start, rather than being slow"
      api_log_this_run | sed 's/^/    /' || true
      die "Studio did not come up. The lines above are the daemon's own account of why."
      ;;
    *)
      warn "the API did not answer on ${API_PORT} within 20s"
      api_log_this_run | sed 's/^/    /' || true
      die "Studio did not come up. The lines above are the daemon's own account of why."
      ;;
  esac

  start_ui
  if wait_for "http://127.0.0.1:${UI_PORT}"; then
    info "  ui   http://localhost:${UI_PORT}"
  else
    warn "the UI container is up but has not answered yet; give it a moment"
  fi

  info ""
  info "Open http://localhost:${UI_PORT}"
  info "  project  ${PROJECT}"
  # Only names the file when the file is what is in force. With --token or
  # $SANDBOX_STUDIO_TOKEN, resolve_token returns before writing it, so pointing
  # at it would name a stale value — and the next `up` without the flag would
  # then run with a different token than the one just described.
  if [ -r "$TOKENFILE" ] && [ "$TOKEN" = "$(cat "$TOKENFILE" 2>/dev/null)" ]; then
    info "  token    ${TOKENFILE}  (already handed to the UI; no need to paste it)"
  else
    info "  token    the one you supplied  (already handed to the UI; not written to disk)"
  fi
  info "  stop     sh studio.sh down"
}

# ---- run ---------------------------------------------------------------------

TMP=$(mktemp -d)
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

case "$CMD" in
  up)        do_up ;;
  down)      do_down ;;
  status)    do_status ;;
  logs)      do_logs ;;
  uninstall) do_uninstall ;;
esac
