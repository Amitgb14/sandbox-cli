# sandbox-base: the default disposable execution environment.
#
# node:*-bookworm-slim is multi-arch, so on Apple Silicon this resolves to the
# arm64 variant automatically. Node is present because every agent wrapped here
# (Claude Code, Codex, Gemini CLI, OpenCode) ships as an npm package.
FROM node:22-bookworm-slim

# Common dev/agent tooling.
RUN apt-get update && apt-get install -y --no-install-recommends \
      git \
      curl \
      ca-certificates \
      ripgrep \
      less \
      openssh-client \
      bash \
      jq \
      python3 \
      build-essential \
      iptables \
      # Goose's installer extracts a .tar.bz2, and GNU tar shells out to bzip2
      # for that. ~100KB, and without it `sandbox-cli goose` cannot install.
      bzip2 \
    && rm -rf /var/lib/apt/lists/*

# Pre-install the agents so `sandbox-cli codex` / `gemini` / `opencode` and an
# offline `sandbox-cli claude` work out of the box. Kept best-effort so an
# upstream hiccup doesn't break the build. The claude wrapper prefers a
# self-updating install in the persisted HOME (~/.local/bin, ahead on PATH); this
# baked copy is the offline fallback. The other wrappers fall the other way: if a
# package is missing here they install it into the persisted HOME on first run.
# Installed one package per layer so a single unavailable agent doesn't take the
# rest down with it — `npm install -g a b c` is all-or-nothing.
RUN npm install -g @anthropic-ai/claude-code || true
RUN npm install -g @openai/codex || true
RUN npm install -g @google/gemini-cli || true
RUN npm install -g opencode-ai || true

# Live resource status line for Claude Code: reads the container's own cgroup v2
# memory/CPU and prints one line. Claude runs it via a managed-settings.json that
# sandbox-cli mounts at /etc/claude-code/managed-settings.json at run time.
RUN cat > /usr/local/bin/sandbox-statusline <<'SL'
#!/bin/bash
# LC_ALL=C so ${#var} counts *bytes*. The line mixes ASCII with multibyte glyphs
# (⬢ · ⎇) whose rendered width is terminal-dependent — "ambiguous width" in
# Unicode, so 1 column in some terminals and 2 in others. A UTF-8 character count
# would therefore under-estimate the width and let the line overflow, which is
# what truncates the right-hand branch. The byte count is an upper bound on the
# real width (an ASCII char is 1 byte / 1 column; a non-ASCII one is >=2 bytes but
# never more than 2 columns), so padding against it can never overflow.
export LC_ALL=C

# Claude pipes the session JSON on stdin; read it (rather than discarding it) so
# the pipe closes cleanly *and* we can pick the branch out of it below.
session=$(cat 2>/dev/null || true)

# termcols reports the width to align against. Claude's session JSON carries no
# terminal geometry, so this is the controlling terminal's width (our own stdin
# is Claude's JSON pipe). SANDBOX_STATUSLINE_COLS overrides it for the case where
# Claude renders the status line into an area narrower than the terminal.
termcols() {
  c=${SANDBOX_STATUSLINE_COLS:-}
  [ -n "$c" ] || c=$({ stty size < /dev/tty; } 2>/dev/null | awk '{print $2}')
  [ -n "$c" ] || c=$(tput cols 2>/dev/null)
  [ -n "$c" ] || c=${COLUMNS:-0}
  printf '%s' "${c:-0}"
}

# Diagnostic: print a column ruler instead of the gauge, so the width Claude
# actually renders into can be *read off* the screen rather than guessed at.
# Run `sandbox-cli claude --env SANDBOX_STATUSLINE_RULER=1` and note the last
# number still visible — that is the usable width.
if [ -n "${SANDBOX_STATUSLINE_RULER:-}" ]; then
  awk -v n="$(termcols)" 'BEGIN{for(i=1;i<=n;i++)printf (i%10==0)?substr(i/10,length(i/10),1):((i%5==0)?"+":".");print ""}'
  exit 0
fi

cg=/sys/fs/cgroup
# Current working-set memory, computed the same way `docker stats` does:
# memory.current minus reclaimable page cache (inactive_file). Usage only — no
# total (the machine total was both misleading and hit a mawk 32-bit overflow).
cur=$(cat "$cg/memory.current" 2>/dev/null || echo 0)
inact=$(awk '/^inactive_file /{print $2; exit}' "$cg/memory.stat" 2>/dev/null || echo 0)
used=$(( cur - inact ))
[ "$used" -ge 0 ] 2>/dev/null || used=$cur
usage=$(awk '/^usage_usec/{print $2; exit}' "$cg/cpu.stat" 2>/dev/null || echo 0)
now=$(date +%s%N 2>/dev/null || echo 0)

state=/tmp/.sandbox-statusline
cpu=""
if [ -r "$state" ]; then
  read -r pu pn < "$state" 2>/dev/null || true
  if [ -n "${pn:-}" ] && [ "$now" -gt "$pn" ] 2>/dev/null; then
    du=$(( usage - pu ))
    dw=$(( (now - pn) / 1000 ))
    if [ "$dw" -gt 0 ] && [ "$du" -ge 0 ]; then
      cpu=$(awk "BEGIN{printf \"%.0f\", ($du/$dw)*100}")
    fi
  fi
fi
printf '%s %s\n' "$usage" "$now" > "$state" 2>/dev/null || true

hum() { awk -v b="${1:-0}" 'BEGIN{split("B KiB MiB GiB TiB",u," ");i=1;while(b>=1024&&i<5){b/=1024;i++}printf (i==1?"%.0f%s":"%.1f%s"),b,u[i]}'; }

# Checked-out branch. Claude's own session JSON reports it when it knows about a
# worktree, so prefer that.
branch=$(printf '%s' "$session" | jq -r '.worktree.branch // empty' 2>/dev/null)

# Otherwise read it straight from .git — cheaper than running git every few
# seconds, and /workspace may be a git worktree whose .git is a pointer file into
# the (bind-mounted) parent repo.
if [ -z "$branch" ]; then
  gd=/workspace/.git
  [ -f "$gd" ] && gd=$(sed -n 's/^gitdir: *//p' "$gd" 2>/dev/null)
  if [ -n "$gd" ] && [ -r "$gd/HEAD" ]; then
    head=$(cat "$gd/HEAD" 2>/dev/null)
    case "$head" in
      "ref: refs/heads/"*) branch=${head#ref: refs/heads/} ;;   # on a branch
      ?*) branch=$(printf '%.7s' "$head") ;;                    # detached: commit id
    esac
  fi
fi

# Subscription usage. Claude's own status-line JSON carries a rate_limits object
# — five_hour and seven_day, each {used_percentage, resets_at} with resets_at in
# epoch seconds — so the numbers come from the same pipe the branch does: no
# extra mount, no file this script has to know the shape of. It is documented as
# present only for Pro/Max accounts and only once the first API response has
# landed, and either window can be missing on its own, so every branch below
# degrades to printing *nothing*. A placeholder next to two real percentages
# reads as a number that failed rather than a limit that isn't being reported.
usage5=""
usage7=""
if [ -z "${SANDBOX_STATUSLINE_NO_USAGE:-}" ]; then
  now_s=$(date +%s 2>/dev/null || echo 0)

  # Time left until a window resets, coarsened to what fits: days once it is more
  # than a day out (the weekly one always is), else hours and minutes. Prints
  # nothing — and so drops the parenthesis entirely — when the timestamp is
  # missing, unparseable, or already past.
  countdown() {
    end=$(awk -v v="${1:-}" 'BEGIN{if(v+0==v && v>0) printf "%d", v}')
    [ -n "$end" ] || return 0
    left=$(( end - now_s ))
    [ "$left" -gt 0 ] 2>/dev/null || return 0
    if [ "$left" -ge 86400 ]; then printf '%dd' $(( left / 86400 ))
    elif [ "$left" -ge 3600 ]; then printf '%dh%02dm' $(( left / 3600 )) $(( (left % 3600) / 60 ))
    else printf '%dm' $(( left / 60 ))
    fi
  }

  # One jq call for both windows, in a fixed order, as "<percent> <resets_at>"
  # per line with "-" for anything absent. Indexing a null rate_limits yields
  # null in jq rather than an error, so an account that reports no limits simply
  # produces two "- -" lines.
  rl=$(printf '%s' "$session" | jq -r '
    .rate_limits as $r
    | ("five_hour", "seven_day")
    | ($r[.] // {})
    | "\(.used_percentage // "-") \(.resets_at // "-")"' 2>/dev/null)

  # Read into the two variables rather than piping: a pipeline runs the loop in a
  # subshell, where the assignments would be thrown away.
  win=0
  while IFS=' ' read -r pct rst; do
    win=$(( win + 1 ))
    # used_percentage may arrive fractional; round it, and treat anything
    # non-numeric (including the "-" above) as this window not being reported.
    p=$(awk -v v="${pct:-}" 'BEGIN{if(v+0==v) printf "%.0f", v}')
    [ -n "$p" ] || continue
    if [ "$win" -eq 1 ]; then
      usage5="5h ${p}%"
      c=$(countdown "$rst")
      [ -n "$c" ] && usage5="$usage5 ($c)"
    else
      # The weekly window resets days out, where a live countdown earns none of
      # the columns it costs. Percent alone.
      usage7="wk ${p}%"
    fi
  done <<< "$rl"
fi

# Which model is answering. Claude reports it in the same JSON as the limits, and
# it belongs next to them: a percentage moves at a rate that depends entirely on
# what is running, and on a plan with per-model caps the two are read together.
# Lowercased to match the rest of the line rather than Claude's own title case.
model=""
if [ -z "${SANDBOX_STATUSLINE_NO_MODEL:-}" ]; then
  model=$(printf '%s' "$session" | jq -r '.model.display_name // empty' 2>/dev/null | tr '[:upper:]' '[:lower:]')
fi

if [ -n "$model" ]; then model=" · $model"; fi

# The fixed part: what sandbox-cli itself measures, always printed.
fixed=" · mem $(hum "$used")"
if [ -n "$cpu" ]; then fixed="$fixed · cpu ${cpu}%"; fi

quota=""
if [ -n "$usage5" ]; then quota="$quota · $usage5"; fi
if [ -n "$usage7" ]; then quota="$quota · $usage7"; fi

# Our stdin is Claude's JSON pipe, so the width comes from the controlling
# terminal instead. SANDBOX_STATUSLINE_COLS overrides it, for when Claude renders
# the status line in an area narrower than the terminal (pass it with --env).
# reserve: leave a few columns free at the right edge. Writing into the last
# column wraps on most terminals, and Claude's own frame may end before the
# terminal does.
cols=$(termcols)
reserve=${SANDBOX_STATUSLINE_RESERVE:-4}
right=""
if [ -n "$branch" ]; then right="⎇ $branch"; fi

# fits reports whether a left half of this many bytes leaves room for the branch.
# An unknown width answers yes: with nothing to measure against, showing a
# segment matches how mem and cpu are already treated — the width caution exists
# for padding to the right edge, which the left half never does.
fits() {
  [ "$cols" -le 0 ] 2>/dev/null && return 0
  need=$(( $1 + reserve ))
  if [ -n "$right" ]; then need=$(( need + ${#right} + 1 )); fi
  [ "$need" -le "$cols" ] 2>/dev/null
}

# Everything the agent reports about itself is droppable, in a fixed order, and
# the branch outlives all of it: percentages age out of usefulness in minutes and
# the model rarely changes mid-session, while which branch the agent is editing
# is why the line was there before either of them. So: try both, then drop the
# usage window (the widest), then the model, and let the branch take what it
# needs from what is left.
line="⬢ sandbox$model$fixed$quota"
if ! fits ${#line}; then
  line="⬢ sandbox$model$fixed"
  if ! fits ${#line}; then
    line="⬢ sandbox$fixed"
  fi
fi

# Push the branch to the right edge.
if [ -n "$right" ]; then
  gap=$(( cols - reserve - ${#line} - ${#right} ))
  # Unknown width, or too narrow for both halves: drop the branch entirely. A
  # half-shown name is worse than none — appending it inline would overflow the
  # row and leave it truncated (or wrapped) mid-name.
  if [ "$gap" -ge 1 ] 2>/dev/null; then
    line="$line$(printf "%${gap}s" "")$right"
  fi
fi
printf '\033[2m%s\033[0m\n' "$line"
SL
RUN chmod +x /usr/local/bin/sandbox-statusline

# Clipboard bridge. There is no clipboard inside the sandbox: no X or Wayland
# display, and pbcopy is a macOS binary that cannot run on Linux at all. The one
# channel that does reach the host is the terminal — OSC 52 is an escape sequence
# the emulator reads off the tty and puts on the real clipboard, so it crosses the
# container boundary as ordinary output bytes, no mount and no extra reach.
#
# Installed under the names tools actually look for (pbcopy, xclip, xsel,
# wl-copy), so anything shelling out to "the clipboard" — Claude Code's /copy, for
# instance — finds one instead of silently failing.
RUN cat > /usr/local/bin/sandbox-clipboard <<'CB'
#!/usr/bin/env bash
set -euo pipefail

# Reading the host clipboard is deliberately not implemented. OSC 52 can request
# it, but the reply has to be parsed back off the tty in raw mode, and terminals
# gate reads behind a prompt or refuse outright — so it fails in ways a caller
# cannot tell apart from an empty clipboard. Failing loudly is more honest.
for arg in "$@"; do
  case "$arg" in
    -o|-out|--output)
      echo "${0##*/}: reading the host clipboard is not supported inside the sandbox" >&2
      exit 1
      ;;
  esac
done

# Text arrives on stdin (pbcopy, xclip, xsel) or as arguments (wl-copy foo).
# Prefer stdin whenever it is not a terminal; reading it with nothing piped in
# would hang waiting for input that never arrives.
if [ ! -t 0 ]; then
  payload=$(base64 -w0)
else
  text=""
  for arg in "$@"; do
    case "$arg" in
      -*) continue ;;                        # flags: -selection, --clipboard, -b
      *)  text="${text:+$text }$arg" ;;
    esac
  done
  payload=$(printf '%s' "$text" | base64 -w0)
fi

# The sequence goes to the controlling terminal, never stdout: the caller is
# usually capturing stdout, and an escape sequence in captured output is a bug.
# Tested by opening it, not with [ -w ] — the device node exists and is writable
# by mode even when there is no controlling terminal to attach to, so the test
# passes and the open then fails with ENXIO.
if ! { exec 3>/dev/tty; } 2>/dev/null; then
  if [ -t 1 ]; then
    exec 3>&1          # no controlling terminal, but stdout is one
  else
    echo "${0##*/}: no terminal to write the clipboard to (is the sandbox running with a TTY?)" >&2
    exit 1
  fi
fi
printf '\033]52;c;%s\a' "$payload" >&3
CB
RUN chmod +x /usr/local/bin/sandbox-clipboard \
    && ln -s sandbox-clipboard /usr/local/bin/pbcopy \
    && ln -s sandbox-clipboard /usr/local/bin/xclip \
    && ln -s sandbox-clipboard /usr/local/bin/xsel \
    && ln -s sandbox-clipboard /usr/local/bin/wl-copy

# Egress allowlist support (used only when sandbox-cli runs with `network:
# allowlist` / `--allow`). sandbox-egress-setup programs a default-deny OUTPUT
# firewall that permits loopback, DNS, established flows, and the resolved IPs of
# the allowlisted domains (passed as SANDBOX_EGRESS_ALLOW). It needs to run as
# root with NET_ADMIN, before the agent starts. Uses plain iptables (no ipset) so
# it works on the common iptables-nft backend without extra kernel modules.
RUN cat > /usr/local/bin/sandbox-egress-setup <<'FW'
#!/bin/bash
# Absolute interpreter and a fixed PATH, both deliberate: this runs as root
# before privileges are dropped, so resolving either through an inherited PATH
# lets whoever controls that PATH choose what root executes. `env bash` and a
# bare `iptables` were exactly that hole.
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
set -euo pipefail

allow="${SANDBOX_EGRESS_ALLOW:-}"
ingress="${SANDBOX_INGRESS_PORTS:-}"

# No IPv6 allowlist yet: reject all IPv6 traffic so it can't be used to bypass the
# IPv4 rules, inbound or outbound. Best-effort — some hosts have no ip6tables.
if command -v ip6tables >/dev/null 2>&1; then
  ip6tables -F OUTPUT 2>/dev/null || true
  ip6tables -A OUTPUT -o lo -j ACCEPT 2>/dev/null || true
  ip6tables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || true
  ip6tables -A OUTPUT -j REJECT 2>/dev/null || true
  ip6tables -F INPUT 2>/dev/null || true
  ip6tables -A INPUT -i lo -j ACCEPT 2>/dev/null || true
  ip6tables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || true
  ip6tables -A INPUT -j REJECT 2>/dev/null || true
fi

iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
# DNS, so the allowlist below (and the agent's own runtime lookups) can resolve.
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT

# Permit the resolved IPv4 addresses of each allowlisted domain. Split the
# comma-separated list into words via `tr` under the default IFS so the inner IP
# loop also splits on whitespace/newlines — setting IFS=',' for the outer loop
# would leak into the inner loop and keep multi-IP getent output as one bad word.
for d in $(printf '%s' "$allow" | tr ',' ' '); do
  [ -z "$d" ] && continue
  for ip in $(getent ahostsv4 "$d" | awk '{print $1}' | sort -u); do
    iptables -A OUTPUT -d "$ip" -j ACCEPT
  done
done

# Everything else outbound is rejected with a fast, visible error (not a hang).
iptables -A OUTPUT -j REJECT --reject-with icmp-port-unreachable

# Ingress. An egress allowlist that ignores inbound traffic is not a boundary:
# the OUTPUT chain accepts ESTABLISHED,RELATED, so a connection someone else
# *dials in* gets a working reply path and carries data straight back out past
# the allowlist. Demonstrated: a second container on the default bridge opened a
# socket to an allowlisted sandbox and received 30 bytes, while that same sandbox
# could not reach 1.1.1.1 on its own initiative.
#
# So the same default-deny shape is applied inbound. Loopback keeps in-container
# tooling working, ESTABLISHED,RELATED keeps replies to the sandbox's *own*
# allowed connections flowing (including DNS answers), and nothing the sandbox
# did not initiate gets in.
#
# FORWARD is deliberately untouched: a container network namespace has one
# interface and routes nothing, so the chain is unreachable.
iptables -F INPUT
iptables -A INPUT -i lo -j ACCEPT
iptables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

# Published ports are the one deliberate exception: `--publish` is an explicit
# request for ingress, so the container ports it names stay reachable. Entries
# arrive as proto:port or proto:lo-hi (see sandbox.IngressPorts); iptables wants
# lo:hi for a range.
for p in $(printf '%s' "$ingress" | tr ',' ' '); do
  [ -z "$p" ] && continue
  proto="${p%%:*}"
  port="$(printf '%s' "${p#*:}" | tr '-' ':')"
  iptables -A INPUT -p "$proto" --dport "$port" -j ACCEPT
done

iptables -A INPUT -j REJECT --reject-with icmp-port-unreachable
FW
RUN chmod +x /usr/local/bin/sandbox-egress-setup

# sandbox-firewall is the container entrypoint used in allowlist mode. It runs as
# root, programs the firewall (failing closed if that errors — better to refuse
# than to run without the requested allowlist), then drops to the intended user
# (SANDBOX_RUN_AS, e.g. `sandbox`) before exec'ing the guest command. With no
# SANDBOX_EGRESS_ALLOW set it is a transparent passthrough.
RUN cat > /usr/local/bin/sandbox-firewall <<'EP'
#!/bin/bash
# Absolute interpreter and a fixed PATH: see sandbox-egress-setup. This script is
# the container's entrypoint and runs as root, so every name it resolves — its own
# interpreter included — must come from the image, never from an inherited PATH
# that the agent's writable HOME or a config `env:` could have supplied.
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
set -euo pipefail

if [ -n "${SANDBOX_EGRESS_ALLOW:-}" ]; then
  if ! /usr/local/bin/sandbox-egress-setup; then
    echo "sandbox-cli: egress firewall setup failed; refusing to run without the requested allowlist" >&2
    exit 1
  fi
fi

user="${SANDBOX_RUN_AS:-}"
if [ -n "$user" ] && [ "$user" != "root" ] && [ "$(/usr/bin/id -u)" = "0" ]; then
  uid="$(/usr/bin/id -u "$user")"
  gid="$(/usr/bin/id -g "$user")"
  exec /usr/bin/setpriv --reuid "$uid" --regid "$gid" --init-groups -- "$@"
fi
exec "$@"
EP
RUN chmod +x /usr/local/bin/sandbox-firewall

# Isolated, non-root user whose home IS /sandbox/home — set as the passwd home
# (-d) so $HOME and getpwuid agree and there is no stray /home/sandbox. Nothing
# here is host-connected: the only host mount is /workspace, injected at run
# time. No explicit UID: the base node image already uses UID 1000, so we let
# useradd pick a free one.
RUN useradd -m -d /sandbox/home -s /bin/bash sandbox \
    && mkdir -p /workspace \
    # Pre-create the package-manager cache dirs so that when `--cache` mounts a
    # fresh named volume over one, docker initializes the volume from this
    # sandbox-owned directory and the non-root user can write to it.
    && mkdir -p /sandbox/home/.npm \
       /sandbox/home/.cache/pip \
       /sandbox/home/.cargo/registry \
       /sandbox/home/go/pkg/mod \
       /sandbox/home/.cache/yarn \
    && chown -R sandbox:sandbox /sandbox/home /workspace

ENV HOME=/sandbox/home
# NOTE: /sandbox/home/.local/bin is deliberately NOT on the image PATH.
#
# That directory is the agent's persisted HOME, bind-mounted read-write from the
# host and writable by the agent itself. With it first on the image PATH it was
# also first on the PATH of the *root* entrypoint used in allowlist mode, so an
# agent could drop a file named `bash` there and have root execute it on the next
# run — container root, and the egress firewall never programmed while sandbox-cli
# reported it was enforcing one.
#
# The agents that want a self-updating install in the persisted HOME add the
# directory themselves, in the guest phase after privileges are dropped
# (claudeBootstrap and agentBootstrap both `export PATH="$HOME/.local/bin:$PATH"`),
# which is where a writable directory belongs.
WORKDIR /workspace

# No USER is set here, so the image itself defaults to root — but sandbox-cli
# runs as the non-root `sandbox` user by default (config.Default), which is safe
# on macOS Docker Desktop where bind ownership is virtualized. Override at run
# time with `--user root` (or `--user uid:gid`).
