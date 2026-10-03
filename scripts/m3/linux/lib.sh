# Shared by the M3 Linux measurement scripts. Source it; do not run it.
#
# Everything lives under $M3_WORK (default /tmp/m3, kept short because unix
# socket paths are limited to ~104 bytes) and every result is appended to
# $M3_RESULTS, which is the file to send back.

set -euo pipefail

M3_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
M3_WORK="${M3_WORK:-/tmp/m3}"
M3_RESULTS="${M3_RESULTS:-$M3_WORK/results-linux.md}"
ARCH="$(uname -m)"
# The fast kernel command line found in the first run: no boot log on the serial
# console, no keyboard-controller probe. 10-boot.sh measures it against the
# defaults; every other script boots with it so it is not measuring the kernel.
M3_FAST_ARGS="quiet i8042.noaux i8042.nomux i8042.nopnp i8042.dumbkbd"
mkdir -p "$M3_WORK/bin"

say()    { printf '\n== %s\n' "$*" >&2; }
die()    { printf 'm3: %s\n' "$*" >&2; exit 1; }
record() { printf -- '- **%s**: %s\n' "$1" "$2" | tee -a "$M3_RESULTS" >&2; }
now_ms() { echo $(( $(date +%s%N) / 1000000 )); }

need_kvm() {
  [ -r /dev/kvm ] && [ -w /dev/kvm ] || die "/dev/kvm is not readable and writable by $(id -un); add yourself to the kvm group (or run as root)"
}

need_root() {
  [ "$(id -u)" -eq 0 ] || die "$1 needs root (tap devices, nftables, the jailer); rerun with sudo"
}

# fetch_firecracker puts firecracker and jailer in $M3_WORK/bin. FC_VERSION pins
# a release (e.g. 1.10.1); unset, the latest release is used and recorded.
fetch_firecracker() {
  if [ -x "$M3_WORK/bin/firecracker" ] && [ -x "$M3_WORK/bin/jailer" ]; then return; fi
  local v="${FC_VERSION:-}"
  if [ -z "$v" ]; then
    v="$(basename "$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/firecracker-microvm/firecracker/releases/latest)")"
    v="${v#v}"
  fi
  say "fetching Firecracker v$v ($ARCH)"
  local t="$M3_WORK/fc.tgz"
  curl -fsSL -o "$t" "https://github.com/firecracker-microvm/firecracker/releases/download/v$v/firecracker-v$v-$ARCH.tgz"
  tar -xzf "$t" -C "$M3_WORK"
  cp "$M3_WORK/release-v$v-$ARCH/firecracker-v$v-$ARCH" "$M3_WORK/bin/firecracker"
  cp "$M3_WORK/release-v$v-$ARCH/jailer-v$v-$ARCH" "$M3_WORK/bin/jailer"
  chmod +x "$M3_WORK/bin/firecracker" "$M3_WORK/bin/jailer"
}

need_kernel() {
  [ -n "${KERNEL:-}" ] || die "set KERNEL=/path/to/vmlinux — a Firecracker guest kernel with CONFIG_IP_PNP and CONFIG_VIRTIO_VSOCKETS, e.g. the one from https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md"
  [ -r "$KERNEL" ] || die "KERNEL=$KERNEL is not readable"
}

# build_rootfs makes $M3_WORK/rootfs.ext4: m3tool as /init and a CA bundle,
# nothing else. No device nodes, so no root needed — init mounts devtmpfs.
build_rootfs() {
  [ -f "$M3_WORK/rootfs.ext4" ] && [ -z "${M3_REBUILD:-}" ] && return
  say "building the guest root filesystem"
  local stage="$M3_WORK/stage"
  rm -rf "$stage"
  mkdir -p "$stage"/{proc,sys,dev,tmp,etc/ssl/certs}
  local goarch
  case "$ARCH" in x86_64) goarch=amd64 ;; aarch64) goarch=arm64 ;; *) die "unsupported arch $ARCH" ;; esac
  (cd "$M3_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -o "$stage/init" ./scripts/m3/m3tool)
  (cd "$M3_ROOT" && go build -o "$M3_WORK/bin/m3tool" ./scripts/m3/m3tool)
  local ca
  for ca in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/cert.pem; do
    [ -r "$ca" ] && { cp "$ca" "$stage/etc/ssl/certs/ca-certificates.crt"; break; }
  done
  [ -s "$stage/etc/ssl/certs/ca-certificates.crt" ] || die "no CA bundle found on this host"
  rm -f "$M3_WORK/rootfs.ext4"
  truncate -s 64M "$M3_WORK/rootfs.ext4"
  mkfs.ext4 -q -F -d "$stage" "$M3_WORK/rootfs.ext4"
}

# write_config <file> <mode> <mem_mib> [net] [vsock]
write_config() {
  local file="$1" mode="$2" mem="$3" net="${4:-}" vs="${5:-}"
  local args="console=ttyS0 reboot=k panic=1 pci=off ro init=/init m3.mode=$mode ${BOOT_ARGS_EXTRA-$M3_FAST_ARGS}"
  [ -n "$net" ] && args="$args ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off"
  {
    printf '{\n  "boot-source": {"kernel_image_path": "%s", "boot_args": "%s"},\n' "${CFG_KERNEL:-$KERNEL}" "$args"
    printf '  "drives": [{"drive_id": "rootfs", "path_on_host": "%s", "is_root_device": true, "is_read_only": true}],\n' "${CFG_ROOTFS:-$M3_WORK/rootfs.ext4}"
    printf '  "machine-config": {"vcpu_count": %s, "mem_size_mib": %s}' "${VCPUS:-2}" "$mem"
    if [ -n "$net" ]; then
      printf ',\n  "network-interfaces": [{"iface_id": "eth0", "guest_mac": "06:00:AC:10:00:02", "host_dev_name": "tap-m3"}]'
    fi
    if [ -n "$vs" ]; then
      printf ',\n  "vsock": {"guest_cid": 3, "uds_path": "%s"}' "${CFG_VSOCK:-$M3_WORK/v.sock}"
    fi
    printf '\n}\n'
  } > "$file"
}

# start_vm <config> <log>: starts Firecracker in the background; sets VM_PID.
start_vm() {
  rm -f "$M3_WORK/api.sock" "$M3_WORK/v.sock"
  "$M3_WORK/bin/firecracker" --api-sock "$M3_WORK/api.sock" --config-file "$1" > "$2" 2>&1 &
  VM_PID=$!
}

# wait_for <log> <pattern> <timeout_s>: returns once the pattern appears.
wait_for() {
  local deadline=$(( $(now_ms) + $3 * 1000 ))
  until grep -q "$2" "$1" 2>/dev/null; do
    kill -0 "${VM_PID:-0}" 2>/dev/null || { tail -20 "$1" >&2; die "the VM exited before printing '$2'"; }
    [ "$(now_ms)" -lt "$deadline" ] || { tail -20 "$1" >&2; die "timed out waiting for '$2'"; }
    sleep 0.002
  done
}

stop_vm() {
  [ -n "${VM_PID:-}" ] && kill "$VM_PID" 2>/dev/null && wait "$VM_PID" 2>/dev/null || true
  VM_PID=
}

# rss_kib <pid>: resident and proportional set size of a process, in KiB.
rss_kib() {
  awk '/^Rss:/{r=$2} /^Pss:/{p=$2} END{printf "rss_kib=%s pss_kib=%s", r, p}' "/proc/$1/smaps_rollup"
}

stats() { # min/median/max of numbers on stdin
  sort -n | awk '{a[NR]=$1} END{printf "min=%s median=%s max=%s n=%d", a[1], a[int((NR+1)/2)], a[NR], NR}'
}

header() {
  if [ ! -s "$M3_RESULTS" ]; then
    {
      echo "# M3 results — Linux"
      echo
      echo "- date: $(date -u +%Y-%m-%dT%H:%MZ)"
      echo "- kernel (host): $(uname -r), arch: $ARCH"
      echo "- cpu: $(awk -F': ' '/model name/{print $2; exit}' /proc/cpuinfo)"
      echo "- firecracker: $("$M3_WORK/bin/firecracker" --version 2>/dev/null | head -1)"
      echo "- guest kernel: $(basename "$KERNEL")"
      echo
    } > "$M3_RESULTS"
  fi
}
