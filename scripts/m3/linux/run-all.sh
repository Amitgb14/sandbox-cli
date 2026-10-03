#!/usr/bin/env bash
# Runs every M3 Linux measurement this user may run, then prints the results
# file to send back. Root-only steps are run when invoked as root, and listed
# as skipped otherwise.
#
#   KERNEL=/path/to/vmlinux ./scripts/m3/linux/run-all.sh          # as yourself
#   sudo KERNEL=/path/to/vmlinux ./scripts/m3/linux/run-all.sh     # everything
source "$(dirname "$0")/lib.sh"
need_kvm; need_kernel; fetch_firecracker; build_rootfs
rm -f "$M3_RESULTS"; header
dir="$(dirname "$0")"
for s in 10-boot 40-vsock 50-snapshot 60-disk; do bash "$dir/$s.sh"; done
if [ "$(id -u)" -eq 0 ]; then
  for s in 20-jailer 30-network; do bash "$dir/$s.sh"; done
else
  record "jailer, network" "skipped: need root (sudo -E $dir/run-all.sh)"
fi
say "results: $M3_RESULTS"
cat "$M3_RESULTS"
