#!/usr/bin/env bash
# Full snapshot and restore: how long each takes, how big the files are, and
# whether the restored guest answers. This decides whether suspend/resume and
# fork (rewrite M8) are cheap enough to be a default. Needs /dev/kvm; not root.
source "$(dirname "$0")/lib.sh"
need_kvm; need_kernel; fetch_firecracker; build_rootfs; header
trap stop_vm EXIT

api() { curl -fsS --unix-socket "$M3_WORK/api.sock" -X "$1" "http://localhost$2" -H 'Content-Type: application/json' ${3:+-d "$3"}; }
mem="${M3_SNAP_MEM:-1024}"

write_config "$M3_WORK/snap.json" vsock "$mem" "" vsock
start_vm "$M3_WORK/snap.json" "$M3_WORK/snap.log"
wait_for "$M3_WORK/snap.log" "M3 VSOCK LISTENING" 30
"$M3_WORK/bin/m3tool" vsock-bench --uds "$M3_WORK/v.sock" --pings 1 --ping-only > /dev/null

rm -f "$M3_WORK/snap.state" "$M3_WORK/snap.mem"
t0=$(now_ms); api PATCH /vm '{"state":"Paused"}'
t1=$(now_ms); api PUT /snapshot/create "{\"snapshot_type\":\"Full\",\"snapshot_path\":\"$M3_WORK/snap.state\",\"mem_file_path\":\"$M3_WORK/snap.mem\"}"
t2=$(now_ms)
record "snapshot: pause (ms)" "$((t1 - t0))"
record "snapshot: create full, ${mem} MiB guest (ms)" "$((t2 - t1))"
record "snapshot: files (apparent / on disk)" "state $(du -h --apparent-size "$M3_WORK/snap.state" | cut -f1)/$(du -h "$M3_WORK/snap.state" | cut -f1), memory $(du -h --apparent-size "$M3_WORK/snap.mem" | cut -f1)/$(du -h "$M3_WORK/snap.mem" | cut -f1)"
stop_vm

say "restore into a fresh VMM"
rm -f "$M3_WORK/api.sock" "$M3_WORK/v.sock"
"$M3_WORK/bin/firecracker" --api-sock "$M3_WORK/api.sock" > "$M3_WORK/restore.log" 2>&1 &
VM_PID=$!
until [ -S "$M3_WORK/api.sock" ]; do sleep 0.002; done
t0=$(now_ms)
api PUT /snapshot/load "{\"snapshot_path\":\"$M3_WORK/snap.state\",\"mem_backend\":{\"backend_type\":\"File\",\"backend_path\":\"$M3_WORK/snap.mem\"},\"resume_vm\":true}"
t1=$(now_ms)
"$M3_WORK/bin/m3tool" vsock-bench --uds "$M3_WORK/v.sock" --pings 1 --ping-only > /dev/null
t2=$(now_ms)
record "restore: load + resume (ms)" "$((t1 - t0))"
record "restore: load -> guest answers over vsock (ms)" "$((t2 - t0))"
record "restore: VMM memory right after restore" "$(rss_kib "$VM_PID")"
