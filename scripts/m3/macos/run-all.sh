#!/usr/bin/env bash
# M3 measurements for the local backend: the native macOS `container` runtime
# (macOS 26+, arm64). Run as yourself — nothing here needs sudo.
#
#   ./scripts/m3/macos/run-all.sh
#
# Every step records what happened and carries on; a step that fails is itself a
# result. The results file is printed at the end — that is what to send back.
# Uses only `container`, a stock alpine image, and tools macOS ships with.

set -uo pipefail

WORK="${M3_WORK:-/tmp/m3}"
RESULTS="${M3_RESULTS:-$WORK/results-macos.md}"
IMG="${M3_IMAGE:-alpine:3.20}"
N="${M3_BOOTS:-10}"
mkdir -p "$WORK"
: > "$RESULTS"

say()    { printf '\n== %s\n' "$*" >&2; }
record() { printf -- '- **%s**: %s\n' "$1" "$2" | tee -a "$RESULTS" >&2; }
now_ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time*1000'; }
stats()  { sort -n | awk '{a[NR]=$1} END{printf "min=%s median=%s max=%s n=%d", a[1], a[int((NR+1)/2)], a[NR], NR}'; }
# try <label> <cmd...>: run, record exit code and the last lines of output.
try() {
  local label="$1"; shift
  local out rc
  out="$("$@" 2>&1)"; rc=$?
  record "$label" "exit=$rc $(printf '%s' "$out" | tail -3 | tr '\n' ' ' | cut -c1-300)"
  return $rc
}
cleanup() {
  container rm -f m3mem m3lbl m3own m3exec >/dev/null 2>&1 || true
}
trap cleanup EXIT

{
  echo "# M3 results — macOS"
  echo
  echo "- date: $(date -u +%Y-%m-%dT%H:%MZ)"
  echo "- macOS: $(sw_vers -productVersion) ($(sw_vers -buildVersion)), arch: $(uname -m)"
  echo "- chip: $(sysctl -n machdep.cpu.brand_string 2>/dev/null)"
  echo "- memory: $(( $(sysctl -n hw.memsize) / 1073741824 )) GiB"
  echo "- container: $(container --version 2>&1 | head -1)"
  echo
} >> "$RESULTS"

say "preflight"
command -v container >/dev/null || { record "preflight" "the container CLI is not installed"; cat "$RESULTS"; exit 1; }
try "preflight: system start" container system start
try "preflight: pull $IMG" container image pull "$IMG"

say "boot time: $N runs of 'true'"
: > "$WORK/run_ms"
for _ in $(seq "$N"); do
  t0=$(now_ms); container run --rm "$IMG" true >/dev/null 2>&1; t1=$(now_ms)
  echo $((t1 - t0)) >> "$WORK/run_ms"
done
record "boot: container run --rm true, wall (ms)" "$(stats < "$WORK/run_ms")"
try "guest kernel" container run --rm "$IMG" uname -a
try "guest kernel netfilter config" container run --rm "$IMG" sh -c 'zcat /proc/config.gz 2>/dev/null | grep -E "^CONFIG_(NETFILTER|NF_TABLES|IP_NF_IPTABLES|NF_NAT|VSOCKETS|VIRTIO_VSOCKETS)=" || echo "no /proc/config.gz"'

say "memory of an idle guest"
container run -d --name m3mem "$IMG" sleep 600 >/dev/null 2>&1
sleep 3
try "memory: container stats" container stats --no-stream m3mem
record "memory: host processes mentioning the container (rss KiB, command)" \
  "$(ps -axo rss=,command= | grep -i -- m3mem | grep -v grep | awk '{print $1, $2}' | head -5 | tr '\n' ';')"
record "memory: largest virtualization processes (rss KiB)" \
  "$(ps -axo rss=,comm= | grep -iE 'virtualization|vmm|container' | sort -rn | head -5 | awk '{print $1, $2}' | tr '\n' ';')"
container rm -f m3mem >/dev/null 2>&1

say "read-only bind mounts"
ro="$WORK/ro"; rm -rf "$ro"; mkdir -p "$ro"
try "ro bind (-v :ro): a guest write must fail" container run --rm -v "$ro:/data:ro" "$IMG" sh -c 'echo x > /data/f && echo WROTE'
try "ro bind (--mount readonly): a guest write must fail" container run --rm --mount "type=bind,source=$ro,target=/data,readonly" "$IMG" sh -c 'echo x > /data/f && echo WROTE'
record "ro bind: file on the host afterwards" "$(ls "$ro" | tr '\n' ' ')(empty means the writes were refused)"

say "ownership of what the guest writes"
own="$WORK/own"; rm -rf "$own"; mkdir -p "$own"
git -C "$own" init -q && git -C "$own" -c user.email=h@m3 -c user.name=h commit -q --allow-empty -m host-first
try "ownership: guest as uid 1001 writes a file and commits" container run --rm -v "$own:/data" "$IMG" sh -c '
  apk add -q git >/dev/null 2>&1 || { echo "apk failed (no network?)"; exit 3; }
  adduser -D -u 1001 sb >/dev/null 2>&1
  su sb -c "echo from-guest > /data/by-guest && git config --global --add safe.directory /data && git -C /data -c user.email=g@m3 -c user.name=g commit -q --allow-empty -m guest && echo committed"
  ls -ln /data /data/.git | head -8'
record "ownership: host view of the guest's file" "$(ls -ln "$own/by-guest" 2>&1)"
try "ownership: host appends to the guest's file" sh -c "echo from-host >> '$own/by-guest'"
try "ownership: host commits after the guest did" git -C "$own" -c user.email=h@m3 -c user.name=h commit -q --allow-empty -m host-after

say "can the guest program a firewall?"
try "firewall: iptables with NET_ADMIN" container run --rm --cap-add NET_ADMIN "$IMG" sh -c '
  apk add -q iptables >/dev/null 2>&1 || { echo "apk failed"; exit 3; }
  iptables -A OUTPUT -p tcp --dport 80 -j REJECT && echo RULE_ADDED
  wget -q -T 4 -O /dev/null http://example.com && echo "STILL_REACHED (rule not enforced)" || echo "BLOCKED (rule enforced)"'
try "firewall: nftables with NET_ADMIN" container run --rm --cap-add NET_ADMIN "$IMG" sh -c '
  apk add -q nftables >/dev/null 2>&1 || { echo "apk failed"; exit 3; }
  nft add table inet t && nft add chain inet t o "{ type filter hook output priority 0; }" && nft add rule inet t o tcp dport 80 reject && echo RULE_ADDED
  wget -q -T 4 -O /dev/null http://example.com && echo "STILL_REACHED" || echo "BLOCKED"'
try "firewall: without NET_ADMIN (must be refused)" container run --rm "$IMG" sh -c 'apk add -q iptables >/dev/null 2>&1; iptables -A OUTPUT -j DROP && echo "ADDED WITHOUT NET_ADMIN"'

say "what the guest can reach by default"
try "network: guest default route" container run --rm "$IMG" ip route
try "network: guest reaches the internet" container run --rm "$IMG" wget -q -T 5 -O /dev/null https://example.com
# A listener on the host, on every address, to see whether the guest reaches the
# host itself — through its gateway, or the host's LAN address.
( nc -lk 18080 >/dev/null 2>&1 & echo $! > "$WORK/nc.pid" )
sleep 0.5
gw="$(container run --rm "$IMG" sh -c "ip route | awk '/default/{print \$3}'" 2>/dev/null)"
lan="$(ipconfig getifaddr en0 2>/dev/null || true)"
try "network: guest reaches a host port via the gateway ($gw)" container run --rm "$IMG" sh -c "nc -z -w 3 $gw 18080 && echo REACHED || echo not-reached"
[ -n "$lan" ] && try "network: guest reaches a host port via the LAN address ($lan)" container run --rm "$IMG" sh -c "nc -z -w 3 $lan 18080 && echo REACHED || echo not-reached"
kill "$(cat "$WORK/nc.pid")" 2>/dev/null || true

say "labels, and what survives a runtime restart"
container run -d --name m3lbl --label sbx.m3=1 "$IMG" sleep 900 >/dev/null 2>&1
try "labels: visible in the listing" sh -c 'container ls --all --format json | grep -o "\"sbx.m3\"[^,}]*" | head -1'
try "labels: runtime stop" container system stop
try "labels: runtime start" container system start
try "labels: still listed after restart" sh -c 'container ls --all --format json | grep -o "m3lbl" | head -1'
try "labels: state after restart" sh -c 'container ls --all | grep m3lbl'

say "what the macos backend relies on"
# The backend renders exactly these; each must work, or it is a backend change.
try "backend: --network none (a sandbox with no network)" container run --rm --network none "$IMG" sh -c 'wget -q -T 3 -O /dev/null https://example.com && echo REACHED || echo no-network'
try "backend: read-only --mount of a host directory" sh -c "container run --rm --mount type=bind,source=$WORK,target=/.sbx,readonly $IMG sh -c 'ls /.sbx >/dev/null && (touch /.sbx/x 2>/dev/null && echo WRITABLE || echo read-only)'"
try "backend: --label and the shape of ls --format json" sh -c 'container ls --all --format json | head -c 1500'
container run -d --name m3exec "$IMG" sleep 300 >/dev/null 2>&1
try "backend: exec --interactive (long flag)" sh -c 'echo hi | container exec --interactive m3exec cat'

say "exec stdio: binary-clean and how fast"
container run -d --name m3own "$IMG" sleep 900 >/dev/null 2>&1
head -c 1048576 /dev/urandom > "$WORK/blob"
want="$(shasum -a 256 < "$WORK/blob" | cut -d' ' -f1)"
got="$(container exec -i m3own cat < "$WORK/blob" | shasum -a 256 | cut -d' ' -f1)"
record "exec -i: 1 MiB of random bytes round-trips intact" "$([ "$want" = "$got" ] && echo yes || echo "NO (sha mismatch)")"
head -c 209715200 /dev/zero > "$WORK/blob200"
t0=$(now_ms); container exec -i m3own sh -c 'cat > /dev/null' < "$WORK/blob200"; t1=$(now_ms)
record "exec -i: 200 MiB into the guest (MB/s)" "$(( 200000 / ( (t1 - t0) > 0 ? (t1 - t0) : 1 ) ))"
t0=$(now_ms); container exec m3own sh -c 'head -c 209715200 /dev/zero' > /dev/null; t1=$(now_ms)
record "exec: 200 MiB out of the guest (MB/s)" "$(( 200000 / ( (t1 - t0) > 0 ? (t1 - t0) : 1 ) ))"
rm -f "$WORK/blob" "$WORK/blob200"

say "results: $RESULTS"
cat "$RESULTS"
