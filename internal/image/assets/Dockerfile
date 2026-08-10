# sandbox-base: the default disposable execution environment.
#
# node:*-bookworm-slim is multi-arch, so on Apple Silicon this resolves to the
# arm64 variant automatically. Node is present because every agent wrapped here
# (Claude Code, Codex, Gemini CLI, OpenCode) ships as an npm package.
# The egress proxy is compiled here rather than on the host: sandbox-cli ships as
# a binary, so requiring a Go toolchain to build the image would be a new
# prerequisite for every user. Its source is written into the build context by
# internal/egressproxy, and the image tag is hashed over that source too, so a
# change to the proxy produces a new tag and a rebuild.
FROM golang:1.25-bookworm AS egressproxy
WORKDIR /src
COPY proxy/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /sandbox-egress-proxy .

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
      # iproute2 for `ip`, which sandbox-egress-setup uses to find the default
      # gateway (to scope published-port ingress) and to detect whether the
      # container has IPv6 at all. Both checks silently did nothing without it —
      # a firewall that cannot see the interface it is filtering.
      iproute2 \
      # Goose's installer extracts a .tar.bz2, and GNU tar shells out to bzip2
      # for that. ~100KB, and without it `sandbox-cli goose` cannot install.
      bzip2 \
    && rm -rf /var/lib/apt/lists/*

# Pre-install the agents so `sandbox-cli codex` / `gemini` / `opencode` and an
# offline `sandbox-cli claude` work out of the box. Kept best-effort so an
# upstream hiccup doesn't break the build. The claude wrapper prefers a
# self-updating install in the persisted HOME (~/.local/bin), which it reaches by
# absolute path — that HOME is agent-writable and shared by every session using
# the adapter, so it is deliberately *appended* to PATH and never prepended, or a
# planted ~/.local/bin/git would shadow this image's for every later run
# (internal/agents/agents.go states the rule at ClaudeBootstrap). Do not "fix"
# that ordering here. This baked copy is the offline fallback, and the only claude
# there is when the install above did not land — it is best-effort, so an image
# built during an npm outage has none, and the run ends at exit 127 rather than
# falling back to something older. The other wrappers fall the other way: if a
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
# sandbox-iptables prints the path of an iptables backend this kernel will
# actually serve, or fails with the kernel's own error.
#
# One definition, three callers: sandbox-egress-setup programs the firewall with
# it, `doctor`'s host probe asks the same question before a run, and the
# integration suite reads rules back through it. Three copies of "which binary
# works" would drift, and the drift is not cosmetic — a preflight that answers
# differently from the launch is the disagreement CLAUDE.md names as the failure
# to avoid.
#
# Why there is a choice at all: Debian points `iptables` at the nft variant,
# which is right on an ordinary host, while gVisor implements only the older
# setsockopt interface and fails nft outright. nft is tried first so an ordinary
# runc host keeps the backend its distribution chose.
#
# It **writes** rather than lists. `-L` only proves the table can be read, and a
# backend that lists perfectly well can still be unable to touch a rule — the
# same argument internal/runtime/docker_cli.go makes for why the host-side probe
# creates rules instead of reading them. Creating and deleting one empty chain is
# the cheapest thing that proves the kernel will accept a write.
#
# On total failure the last candidate's stderr goes to ours, unswallowed: on a
# rootless or userns-remapped daemon that line is "Permission denied (you must be
# root)", which names the real cause. A caller that printed only its own remedy
# would send those users chasing the wrong one.
RUN cat > /usr/local/bin/sandbox-iptables <<'PICK'
#!/bin/bash
# Absolute interpreter and a fixed PATH: this is called from the root phase, so
# every name it resolves must come from the image. See sandbox-egress-setup.
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
set -uo pipefail

case "${1:-}" in
  -6) candidates="ip6tables ip6tables-legacy" ;;
  "") candidates="iptables iptables-legacy" ;;
  *)  echo "usage: sandbox-iptables [-6]" >&2; exit 2 ;;
esac

chain="SANDBOX_BACKEND_PROBE"
last=""
for name in $candidates; do
  resolved="$(command -v "$name" 2>/dev/null)" || continue
  if last="$("$resolved" -N "$chain" 2>&1)"; then
    "$resolved" -X "$chain" >/dev/null 2>&1 || true
    printf '%s\n' "$resolved"
    exit 0
  fi
done
[ -n "$last" ] && printf '%s\n' "$last" >&2
exit 1
PICK
RUN chmod +x /usr/local/bin/sandbox-iptables

# root with NET_ADMIN, before the agent starts. Uses plain iptables (no ipset) so
# it needs no extra kernel modules, and picks the backend the kernel will serve
# rather than assuming one — see sandbox-iptables.
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

# Which iptables backend this kernel will serve. See sandbox-iptables; the
# choice is made there so `doctor` and the integration suite ask it the same way.
if ! IPTABLES="$(/usr/local/bin/sandbox-iptables)"; then
  echo "sandbox-cli: no working iptables backend in this container, so the egress" >&2
  echo "sandbox-cli: allowlist cannot be programmed; refusing to run without it" >&2
  echo "sandbox-cli: the line above is the kernel's own error and names the cause;" >&2
  echo "sandbox-cli: under gVisor, iptables needs \`runsc install -- --net-raw\`" >&2
  exit 1
fi
# Recoverable from `docker logs`: IPv4 and IPv6 are chosen independently, so a
# container can hold its v4 rules in one store and its v6 rules in the other, and
# an operator reading back with the wrong binary sees an empty chain and concludes
# nothing was programmed.
echo "sandbox-cli: firewall backend $IPTABLES" >&2

# Whether this backend can match connection state.
#
# gVisor's netstack provides no connection tracking at all — neither
# `-m conntrack` nor the older `-m state` — while `owner` matching and nat
# REDIRECT both work. So the allowlist is buildable there, but not the way it is
# written by default, and the difference has to be detected rather than assumed:
# it is a property of the kernel the container got, not of the engine or the
# binary.
#
# Tried on a scratch chain, like everything else here. A rule that cannot be
# added to a chain nobody consults is the cheapest way to ask.
#
# Asked once per extension, up front, rather than discovered one failed run at a
# time — which is how this was found: conntrack was probed, `-m limit` and
# `-j LOG` were not, and the second wall only appeared after the first was
# cleared.
rule_ok() { # rule_ok BINARY RULE...
  local ipt="$1" chain="SANDBOX_FEATURE_PROBE"
  shift
  "$ipt" -N "$chain" >/dev/null 2>&1 || true
  if "$ipt" -A "$chain" "$@" >/dev/null 2>&1; then
    "$ipt" -F "$chain" >/dev/null 2>&1 || true
    "$ipt" -X "$chain" >/dev/null 2>&1 || true
    return 0
  fi
  "$ipt" -F "$chain" >/dev/null 2>&1 || true
  "$ipt" -X "$chain" >/dev/null 2>&1 || true
  return 1
}

# Rate-limited logging of what the firewall refused. Diagnostics, not
# enforcement: without it the rules are identical and only the explanation is
# missing, so it degrades rather than refusing. gVisor has neither `-m limit`
# nor `-j LOG`.
if rule_ok "$IPTABLES" -m limit --limit 1/min -j LOG; then
  DENYLOG=1
else
  DENYLOG=0
  echo "sandbox-cli: this kernel cannot log refused packets (no -m limit / -j LOG), so" >&2
  echo "sandbox-cli: denials are enforced but not counted or explained in the run log." >&2
fi

# How to refuse. REJECT answers with an ICMP error so a blocked client fails
# immediately instead of hanging until its timeout, which is worth a lot when the
# blocked client is an agent waiting on a request. Where the target is missing,
# DROP denies exactly as much — the difference is the courtesy, not the boundary.
if rule_ok "$IPTABLES" -j REJECT --reject-with icmp-port-unreachable; then
  DENY="-j REJECT --reject-with icmp-port-unreachable"
  DENY6="-j REJECT"
else
  DENY="-j DROP"
  DENY6="-j DROP"
  echo "sandbox-cli: this kernel has no REJECT target, so refused traffic is dropped" >&2
  echo "sandbox-cli: silently; a blocked client waits for its own timeout instead of" >&2
  echo "sandbox-cli: failing at once. The same traffic is refused either way." >&2
fi

if rule_ok "$IPTABLES" -m conntrack --ctstate ESTABLISHED -j ACCEPT; then
  CONNTRACK=1
else
  CONNTRACK=0
  echo "sandbox-cli: this kernel has no connection tracking, so the egress allowlist is" >&2
  echo "sandbox-cli: enforced by uid and destination instead of by connection state." >&2
  echo "sandbox-cli: OUTBOUND filtering is unchanged: only the proxy may leave, and the" >&2
  echo "sandbox-cli: guest's 80/443 is still redirected into it." >&2
  echo "sandbox-cli: INBOUND filtering is NOT applied — a stateless chain cannot tell a" >&2
  echo "sandbox-cli: reply from an unsolicited connection, and dropping both would break" >&2
  echo "sandbox-cli: every allowed request. Nothing inside can answer an inbound" >&2
  echo "sandbox-cli: connection, because the answer would leave from a denied uid." >&2
fi

# No IPv6 allowlist yet: reject all IPv6 traffic so it can't be used to bypass the
# IPv4 rules, inbound or outbound.
#
# This used to be best-effort in both directions — every call was `|| true`, so a
# missing ip6tables left IPv6 completely unfiltered while sandbox-cli reported it
# was enforcing an allowlist. Failing open is the one thing the rest of this
# script is careful not to do. Now: if the container has IPv6 at all, the rules
# are required; if it has none, there is nothing to filter and nothing to fail.
if ip -6 addr show scope global 2>/dev/null | grep -q inet6; then
  if ! IP6TABLES="$(/usr/local/bin/sandbox-iptables -6)"; then
    echo "sandbox-cli: this container has IPv6 but no working ip6tables backend, so the" >&2
    echo "sandbox-cli: allowlist cannot be enforced on it; refusing rather than filtering only IPv4" >&2
    exit 1
  fi
  "$IP6TABLES" -F OUTPUT
  "$IP6TABLES" -A OUTPUT -o lo -j ACCEPT
  [ "$CONNTRACK" = 1 ] && "$IP6TABLES" -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT || true
  "$IP6TABLES" -A OUTPUT $DENY6
  "$IP6TABLES" -F INPUT
  "$IP6TABLES" -A INPUT -i lo -j ACCEPT
  [ "$CONNTRACK" = 1 ] && "$IP6TABLES" -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT || true
  "$IP6TABLES" -A INPUT $DENY6
elif IP6TABLES="$(/usr/local/bin/sandbox-iptables -6)"; then
  # No global IPv6 address, but keep the rules if we can: an address could be
  # configured later, and a default-deny chain costs nothing.
  "$IP6TABLES" -F OUTPUT 2>/dev/null || true
  "$IP6TABLES" -A OUTPUT -o lo -j ACCEPT 2>/dev/null || true
  "$IP6TABLES" -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || true
  "$IP6TABLES" -A OUTPUT $DENY6 2>/dev/null || true
  "$IP6TABLES" -F INPUT 2>/dev/null || true
  "$IP6TABLES" -A INPUT -i lo -j ACCEPT 2>/dev/null || true
  "$IP6TABLES" -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || true
  "$IP6TABLES" -A INPUT $DENY6 2>/dev/null || true
fi

"$IPTABLES" -F OUTPUT
"$IPTABLES" -A OUTPUT -o lo -j ACCEPT
# A packet redirected into the local proxy has its destination rewritten to
# 127.0.0.1, but does NOT necessarily leave via `lo` — the routing decision was
# already made before the nat OUTPUT chain ran. Matching on the destination
# instead is what actually covers it: without this the REDIRECT succeeded and the
# rewritten packet was then rejected by the rule at the bottom of this chain, so
# every connection failed with the proxy sitting idle and logging nothing.
# 127.0.0.0/8 is this container's own loopback, so this widens nothing.
"$IPTABLES" -A OUTPUT -d 127.0.0.0/8 -j ACCEPT
# Replies to our own connections. On a backend without connection tracking this
# is omitted and the rules below carry the whole chain: the proxy's packets match
# on its uid — including the ACKs and retransmits the kernel generates, measured
# — and the guest's redirected traffic is loopback-bound by the rule above.
if [ "$CONNTRACK" = 1 ]; then
  "$IPTABLES" -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
fi
# DNS, so the allowlist below (and the agent's own runtime lookups) can resolve —
# but only to the resolvers this container was actually given.
#
# Accepting port 53 to *any* destination is not "allow DNS", it is a full-duplex
# tunnel to any host willing to listen on 53: a peer container was reached on
# port 53 while every other port on it was refused, and public resolvers answered
# arbitrary attacker-chosen queries. Restricting to /etc/resolv.conf turns that
# back into DNS. Name-based exfiltration through the real resolver remains
# possible — that needs a proxy, not a firewall rule — but the arbitrary-peer
# channel does not.
for ns in $(awk '/^nameserver/ {print $2}' /etc/resolv.conf 2>/dev/null); do
  case "$ns" in
    *:*) continue ;;  # IPv6 resolver; IPv6 egress is rejected wholesale above
  esac
  "$IPTABLES" -A OUTPUT -p udp -d "$ns" --dport 53 -j ACCEPT
  "$IPTABLES" -A OUTPUT -p tcp -d "$ns" --dport 53 -j ACCEPT
done

# Name-based enforcement, when the proxy is available.
#
# The rules below this point match resolved ADDRESSES, and an address is not a
# name: gist.github.com answers because it shares github.com's address, and a
# rotating record breaks a domain that was explicitly allowed. The proxy decides
# on the name the client actually asked for, freshly, per connection.
#
# The lever is uid ownership. Outbound traffic is permitted only from the proxy's
# own uid; everything else on 80/443 is redirected INTO the proxy. The agent
# cannot go around it, because going around it would mean being a different user.
# Everything not 80/443 still falls through to the address rules and the final
# REJECT, so this narrows what is reachable and never widens it.
if [ -n "${SANDBOX_PROXY_PORT:-}" ] && command -v sandbox-egress-proxy >/dev/null 2>&1; then
  proxy_uid="$(id -u sandbox-proxy 2>/dev/null || echo)"
  if [ -n "$proxy_uid" ]; then
    "$IPTABLES" -t nat -N SANDBOX_EGRESS 2>/dev/null || "$IPTABLES" -t nat -F SANDBOX_EGRESS
    # The proxy's own connections must not be redirected back into itself.
    "$IPTABLES" -t nat -A SANDBOX_EGRESS -m owner --uid-owner "$proxy_uid" -j RETURN
    "$IPTABLES" -t nat -A SANDBOX_EGRESS -p tcp --dport 80 -j REDIRECT --to-ports "$SANDBOX_PROXY_PORT"
    "$IPTABLES" -t nat -A SANDBOX_EGRESS -p tcp --dport 443 -j REDIRECT --to-ports "$SANDBOX_PROXY_PORT"
    # -C then -A: the chain above is created-or-flushed, but the jump into it
    # lives in OUTPUT and would stack a duplicate every time this ran. One run
    # per container makes that harmless today, which is exactly why it would go
    # unnoticed the first time the setup is re-run.
    "$IPTABLES" -t nat -C OUTPUT -j SANDBOX_EGRESS 2>/dev/null \
      || "$IPTABLES" -t nat -A OUTPUT -j SANDBOX_EGRESS
    # Loopback must be reachable so the redirect has somewhere to land, and the
    # proxy itself is allowed out — it is the thing enforcing the allowlist.
    "$IPTABLES" -A OUTPUT -m owner --uid-owner "$proxy_uid" -j ACCEPT
    echo "sandbox-cli: egress enforced by name (proxy on 127.0.0.1:$SANDBOX_PROXY_PORT)" >&2
  fi
fi

# Permit the resolved IPv4 addresses of each allowlisted domain. Split the
# comma-separated list into words via `tr` under the default IFS so the inner IP
# loop also splits on whitespace/newlines — setting IFS=',' for the outer loop
# would leak into the inner loop and keep multi-IP getent output as one bad word.
for d in $(printf '%s' "$allow" | tr ',' ' '); do
  [ -z "$d" ] && continue
  for ip in $(getent ahostsv4 "$d" | awk '{print $1}' | sort -u); do
    "$IPTABLES" -A OUTPUT -d "$ip" -j ACCEPT
  done
done

# Everything else outbound is rejected with a fast, visible error (not a hang) —
# and logged first. A blocked connection used to leave no trace anywhere: the
# agent saw a connection error it may well swallow, and the user had no way to
# answer "what did the sandbox try to reach, and what was refused?". That is the
# question an allowlist exists to be able to answer.
#
# --log-prefix is what makes it greppable; the rate limit is what stops a busy
# agent filling the kernel ring buffer (and the host's syslog on Linux, where the
# container shares it) with one line per retry.
if [ "$DENYLOG" = 1 ]; then
  "$IPTABLES" -A OUTPUT -m limit --limit 12/min --limit-burst 20 \
    -j LOG --log-prefix "sandbox-cli DENY-OUT " --log-level 4
fi
# shellcheck disable=SC2086 # $DENY is several words by design
"$IPTABLES" -A OUTPUT $DENY

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
# Ingress filtering needs connection state: the chain has to admit replies to
# connections the sandbox opened while refusing everything unsolicited, and
# without conntrack those are indistinguishable. Denying both would reject every
# reply and break each allowed request; admitting both is what a default-ACCEPT
# INPUT already does, with no rule to write.
#
# What holds the boundary in that case is the OUTPUT chain above: nothing but the
# proxy may send anything outward, so an inbound connection cannot be answered —
# the answer would leave from a denied uid and the handshake never completes.
# That is the containment argument, and it is why this is a documented
# limitation of stateless kernels rather than a hole. See
# docs/roadmap/task-3-gvisor-egress.md.
if [ "$CONNTRACK" = 1 ]; then
  "$IPTABLES" -F INPUT
  "$IPTABLES" -A INPUT -i lo -j ACCEPT
  "$IPTABLES" -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

  # Published ports are the one deliberate exception: `--publish` is an explicit
  # request for ingress, so the container ports it names stay reachable. Entries
  # arrive as proto:port or proto:lo-hi (see sandbox.IngressPorts); iptables wants
  # lo:hi for a range.
  # Only from the bridge gateway, which is where docker's own port forwarding
  # arrives from. Accepting a published port from *any* source contradicted the
  # thing that makes --publish safe: sandbox.NormalizePublish deliberately binds
  # the HOST side to 127.0.0.1 unless told otherwise, and then the container
  # accepted the same port from every other container on the bridge — a peer read
  # workspace data out of a sandbox whose port the user had published to loopback
  # only.
  # || true: under set -e a failing substitution would abort the whole setup, and
  # a missing default route is not a reason to refuse to run.
  gw="$(ip route 2>/dev/null | awk '/^default/ {print $3; exit}' || true)"
  # The container's own bridge subnet — the address range its peers live in, and
  # the only range that must not reach a published port.
  #
  # Anchoring on the gateway alone assumed docker's userland proxy, which
  # re-originates the connection from the gateway address. With
  # "userland-proxy": false (common on Linux servers) it is pure DNAT: the source
  # stays the original client, which is 127.0.0.1 for the loopback-bound publish
  # that is sandbox-cli's default, and an arbitrary external address otherwise.
  # Neither matches "$gw", so the published port would silently stop answering
  # under --allow — the exact regression these rules were written to avoid.
  #
  # So the property is expressed directly instead of by proxy: everything may
  # reach a published port except this container's bridge peers.
  #
  # That is a denylist where the old rule was an allowlist, and the difference is
  # worth stating: a container on a *different* docker bridge is not in $subnet, so
  # these rules now accept it where gateway-only would have denied it. Docker's own
  # DOCKER-ISOLATION-STAGE-* chains block inter-bridge traffic, so this leans on
  # that rather than opening a path — but it is defence in depth given up, and if
  # docker ever stops isolating bridges by default this needs revisiting.
  #
  # A malformed $subnet would make the iptables call fail and, under `set -e`,
  # refuse the run. That is the right direction, if an opaque one: the alternative
  # is a published port with no peer filtering at all.
  subnet="$(ip route 2>/dev/null | awk '/proto kernel/ && /src/ {print $1; exit}' || true)"
  for p in $(printf '%s' "$ingress" | tr ',' ' '); do
    [ -z "$p" ] && continue
    proto="${p%%:*}"
    port="$(printf '%s' "${p#*:}" | tr '-' ':')"
    # The gateway is inside the subnet, so it is admitted before peers are denied.
    [ -n "$gw" ] && "$IPTABLES" -A INPUT -s "$gw" -p "$proto" --dport "$port" -j ACCEPT
    # Pure DNAT of a loopback-bound publish arrives on eth0 with a 127/8 source
    # (route_localnet), so the `-i lo` rule above does not cover it.
    "$IPTABLES" -A INPUT -s 127.0.0.0/8 -p "$proto" --dport "$port" -j ACCEPT
    if [ -n "$subnet" ]; then
      "$IPTABLES" -A INPUT -s "$subnet" -p "$proto" --dport "$port" -j DROP
    fi
    # Everyone else: publishing is an explicit request for ingress.
    "$IPTABLES" -A INPUT -p "$proto" --dport "$port" -j ACCEPT
  done

  if [ "$DENYLOG" = 1 ]; then
    "$IPTABLES" -A INPUT -m limit --limit 12/min --limit-burst 20 \
      -j LOG --log-prefix "sandbox-cli DENY-IN " --log-level 4
  fi
  "$IPTABLES" -A INPUT $DENY
else
  "$IPTABLES" -P INPUT ACCEPT
fi
FW
RUN chmod +x /usr/local/bin/sandbox-egress-setup

# sandbox-init is the image's default ENTRYPOINT and the last thing that runs
# before the guest, on every run. It exists for one setting that has nowhere else
# to live: the umask.
#
# A umask is a property of a process, not of the filesystem, so no Dockerfile
# directive and no docker flag can set it (podman has --umask; docker does not).
# It is inherited from whatever started the container, which is 0022 — and 0022
# is wrong whenever the host and the container are sharing a group to reach the
# same files, because it strips the group-write bit off everything the container
# creates. See internal/sandbox/hostgroup.go for why that group exists.
#
# Deliberately unprivileged: this is the *default* entrypoint, used on runs that
# do not ask for an egress allowlist, where docker has already applied --user
# before the first line here. Allowlist mode replaces it with sandbox-firewall,
# which starts as root and calls back into this script after dropping.
RUN cat > /usr/local/bin/sandbox-init <<'IN'
#!/bin/bash
# Absolute interpreter, for the same reason sandbox-egress-setup has one.
#
# It deliberately does **not** reset PATH, and that is the difference between
# this script and the two that do. Those run as root before the privilege drop,
# where every name they resolve must come from the image. This one runs *after*
# the drop, as the guest user, and is the image's default entrypoint — so it
# stands between docker and every command sandbox-cli runs. Resetting PATH here
# would not harden anything (the caller already has the guest's privileges); it
# would only discard the PATH the image or a config `env:` set, and a run that
# worked before this script existed would fail with "command not found".
#
# Nothing below needs a PATH: umask, echo, [ and exec are bash builtins, and the
# one name resolved through it is the guest command, which is exactly what docker
# would have resolved the same way with no entrypoint at all.
set -euo pipefail

# SANDBOX_UMASK is reserved (config.IsReservedEnv), so the only writer is
# sandbox-cli and only when it has decided a shared group is in play. It is still
# matched against a literal octal shape rather than passed through: `umask` on a
# malformed value fails, and under `set -e` that would turn a cosmetic setting
# into a container that does not start. An unrecognized value is ignored for the
# same reason — the guest running with the default mask is a worse run, not a
# failed one.
case "${SANDBOX_UMASK:-}" in
  [0-7][0-7][0-7]|[0-7][0-7][0-7][0-7]) umask "$SANDBOX_UMASK" ;;
esac

# The image carries no CMD, so an empty argv means the caller gave none. `exec`
# with no arguments would silently exit 0 and read as a container that ran and
# succeeded.
if [ "$#" -eq 0 ]; then
  echo "sandbox-cli: no command given" >&2
  exit 1
fi

exec "$@"
IN
RUN chmod +x /usr/local/bin/sandbox-init

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

# Start the egress proxy before dropping privileges, as its own user. It must be
# running before the guest does, or the redirect rules point at nothing and every
# outbound connection is refused — so a failure to start is a failure to run,
# matching the rest of this script's fail-closed posture.
if [ -n "${SANDBOX_PROXY_PORT:-}" ] && [ -n "${SANDBOX_EGRESS_ALLOW:-}" ] \
   && command -v sandbox-egress-proxy >/dev/null 2>&1 && [ "$(/usr/bin/id -u)" = "0" ]; then
  SANDBOX_PROXY_LISTEN="127.0.0.1:$SANDBOX_PROXY_PORT" \
    /usr/bin/setpriv --reuid sandbox-proxy --regid nogroup --clear-groups \
      /usr/local/bin/sandbox-egress-proxy &
  # Wait briefly for the listener; the guest starting first would see refusals.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    if (exec 3<>/dev/tcp/127.0.0.1/"$SANDBOX_PROXY_PORT") 2>/dev/null; then break; fi
    sleep 0.2
  done
fi

# Everything below hands the guest to sandbox-init rather than exec'ing it
# directly, because this script *replaces* that entrypoint in allowlist mode: a
# setting applied in only one of the two would hold for exactly half of the runs.
# That is the same trap sharedGroupUser avoids by applying to SANDBOX_RUN_AS as
# well as to --user, and the reason the umask is not simply set here is that it
# would then also apply to the root phase above, which has no business creating
# group-writable files.
user="${SANDBOX_RUN_AS:-}"
if [ -n "$user" ] && [ "$(/usr/bin/id -u)" = "0" ]; then
  # SANDBOX_RUN_AS is whatever --user was given, so it is a name OR a numeric
  # uid[:gid] — `docker run --user 1000:1000` is documented, and `id -u 1000:1000`
  # simply fails, which under `set -e` killed the run with an opaque
  # "id: '1000:1000': no such user" before the guest ever started.
  case "$user" in
    *[!0-9:]*)
      # A name: ask the passwd database.
      uid="$(/usr/bin/id -u "$user")"
      gid="$(/usr/bin/id -g "$user")"
      ;;
    *)
      # Numeric uid or uid:gid; a bare uid reuses itself as the gid, matching
      # docker's own reading of --user.
      uid="${user%%:*}"
      gid="${user#*:}"
      [ "$gid" = "$user" ] && gid="$uid"
      ;;
  esac
  # Dropping to root is not dropping. Checked on the resolved id rather than the
  # spelling, so `--user 0` and `--user root` behave the same.
  if [ "$uid" != "0" ]; then
    # --init-groups reads the passwd/group database, so it fails outright for a
    # numeric uid with no entry — and `--user $(id -u)` is the standard way on
    # Linux to get bind-mount ownership right, which made correct ownership and
    # the egress allowlist mutually exclusive. Ask for supplementary groups only
    # when there is somewhere to read them from.
    if /usr/bin/id -u "$user" >/dev/null 2>&1; then
      exec /usr/bin/setpriv --reuid "$uid" --regid "$gid" --init-groups -- \
        /usr/local/bin/sandbox-init "$@"
    fi
    exec /usr/bin/setpriv --reuid "$uid" --regid "$gid" --clear-groups -- \
      /usr/local/bin/sandbox-init "$@"
  fi
fi
exec /usr/local/bin/sandbox-init "$@"
EP
RUN chmod +x /usr/local/bin/sandbox-firewall

# The egress proxy and the uid it runs as.
#
# Its own uid is the enforcement lever: the firewall permits outbound traffic
# ONLY from this uid and redirects everything else to the proxy, so the agent
# cannot go around it — "around it" would mean being a different user. It owns
# nothing writable and has no shell.
COPY --from=egressproxy /sandbox-egress-proxy /usr/local/bin/sandbox-egress-proxy
RUN chmod 0755 /usr/local/bin/sandbox-egress-proxy \
    && useradd --system --no-create-home --shell /usr/sbin/nologin sandbox-proxy

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

# Declared on the image rather than rendered as `--entrypoint` on every run, and
# that is the whole reason this works: sandbox-cli's argv does not change, so a
# user-supplied `image:` — which may have no sandbox-init and is none of our
# business — is not handed an entrypoint it cannot run. Allowlist mode overrides
# this with sandbox-firewall, which calls back into it.
#
# One consequence to know about: docker reports an entrypoint as part of a
# container's command, so this shows up in `docker inspect` and in everything
# built on it. runtime.guestCommand strips both wrappers back off, which is also
# where sandbox-firewall stopped appearing there.
ENTRYPOINT ["/usr/local/bin/sandbox-init"]
