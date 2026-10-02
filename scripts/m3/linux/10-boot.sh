#!/usr/bin/env bash
# Boot time and memory of a Firecracker microVM. Needs /dev/kvm; not root.
#
# Boot: host_ready_ms is from starting the VMM to the guest's init printing its
# first line — what a sandbox create would wait for. guest_ms is the guest
# kernel's own uptime at that moment. Memory: the VMM process's resident and
# proportional set size with the guest idle, at two memory sizes, since guest
# memory is only touched when used.
source "$(dirname "$0")/lib.sh"
need_kvm; need_kernel; fetch_firecracker; build_rootfs; header

# Three kernel command lines, because the difference between them is most of the
# boot: the kernel's own log over the emulated serial port, and probing the
# emulated keyboard controller (which reboot=k still uses to end the VM).
FAST="$M3_FAST_ARGS"
boot_series() { # <label> <extra boot args>
  say "boot time, $1 (${M3_BOOTS:-10} boots)"
  BOOT_ARGS_EXTRA="$2" write_config "$M3_WORK/boot.json" boot 512
  : > "$M3_WORK/host_ms" ; : > "$M3_WORK/guest_ms" ; : > "$M3_WORK/exit_ms"
  for i in $(seq "${M3_BOOTS:-10}"); do
    log="$M3_WORK/boot-$i.log"
    t0=$(now_ms); start_vm "$M3_WORK/boot.json" "$log"
    wait_for "$log" "M3 READY" 30; t1=$(now_ms)
    wait "$VM_PID" 2>/dev/null || true; t2=$(now_ms); VM_PID=
    echo $((t1 - t0)) >> "$M3_WORK/host_ms"
    sed -n 's/.*M3 READY uptime_ms=\([0-9]*\).*/\1/p' "$log" | head -1 >> "$M3_WORK/guest_ms"
    echo $((t2 - t0)) >> "$M3_WORK/exit_ms"
  done
  record "boot [$1]: host start -> guest init ready (ms)" "$(stats < "$M3_WORK/host_ms")"
  record "boot [$1]: guest uptime at init (ms)" "$(stats < "$M3_WORK/guest_ms")"
  record "boot [$1]: host start -> VM gone after guest reboot (ms)" "$(stats < "$M3_WORK/exit_ms")"
}
boot_series "default args" ""
boot_series "quiet" "quiet"
boot_series "quiet, no i8042 probe" "$FAST"

for mem in 512 2048; do
  say "memory, idle guest, ${mem} MiB"
  BOOT_ARGS_EXTRA="$FAST" write_config "$M3_WORK/mem.json" vsock "$mem" "" vsock
  start_vm "$M3_WORK/mem.json" "$M3_WORK/mem.log"
  wait_for "$M3_WORK/mem.log" "M3 VSOCK LISTENING" 30
  sleep 2
  record "memory: VMM process, idle guest, ${mem} MiB configured" "$(rss_kib "$VM_PID")"
  stop_vm
done
