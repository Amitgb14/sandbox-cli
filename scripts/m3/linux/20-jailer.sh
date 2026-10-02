#!/usr/bin/env bash
# The same boot under the jailer: chroot, its own mount and PID namespaces, and a
# non-root uid. Needs root — which is the reason the plan runs Linux sandboxd as
# a system service. Records whether it boots, how long it takes, and the uid the
# VMM actually ran as.
source "$(dirname "$0")/lib.sh"
need_root "the jailer"; need_kvm; need_kernel; fetch_firecracker; build_rootfs; header

id=m3jail
base="$M3_WORK/jail"
root="$base/firecracker/$id/root"
uid="${JAIL_UID:-65534}"
cleanup() { pkill -f -- "--id $id" 2>/dev/null || true; rm -rf "$base"; }
trap cleanup EXIT
cleanup
mkdir -p "$root"
cp "$KERNEL" "$root/vmlinux"
cp "$M3_WORK/rootfs.ext4" "$root/rootfs.ext4"
chown "$uid:$uid" "$root/vmlinux" "$root/rootfs.ext4"
CFG_KERNEL=/vmlinux CFG_ROOTFS=/rootfs.ext4 write_config "$root/config.json" vsock 512

say "boot under the jailer as uid $uid"
log="$M3_WORK/jail.log"
t0=$(now_ms)
"$M3_WORK/bin/jailer" --id "$id" --exec-file "$M3_WORK/bin/firecracker" \
  --uid "$uid" --gid "$uid" --chroot-base-dir "$base" \
  -- --config-file /config.json > "$log" 2>&1 &
VM_PID=$!
wait_for "$log" "M3 READY" 30
t1=$(now_ms)
fc_pid="$(pgrep -f -- "--id $id" | head -1)"
record "jailer: boots" "yes, host start -> guest ready $((t1 - t0)) ms"
record "jailer: VMM runs as" "$(ps -o uid=,user= -p "$(pgrep -P "$fc_pid" 2>/dev/null || echo "$fc_pid")" | head -1 | xargs)"
record "jailer: /dev/kvm inside the chroot" "$(ls -l "$root/dev/kvm" 2>&1 | awk '{print $1, $3, $4}')"
stop_vm
