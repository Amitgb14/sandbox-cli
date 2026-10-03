#!/usr/bin/env bash
# The host<->guest channel the guest agent will use: connection round trip, and
# throughput each way. Needs /dev/kvm; not root.
source "$(dirname "$0")/lib.sh"
need_kvm; need_kernel; fetch_firecracker; build_rootfs; header
trap stop_vm EXIT

write_config "$M3_WORK/vsock.json" vsock 1024 "" vsock
start_vm "$M3_WORK/vsock.json" "$M3_WORK/vsock.log"
wait_for "$M3_WORK/vsock.log" "M3 VSOCK LISTENING" 30
"$M3_WORK/bin/m3tool" vsock-bench --uds "$M3_WORK/v.sock" --mb "${M3_VSOCK_MB:-512}" | while read -r _ _ rest; do record "vsock" "$rest"; done
