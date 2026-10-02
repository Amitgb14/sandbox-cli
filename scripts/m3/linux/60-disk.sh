#!/usr/bin/env bash
# What a per-sandbox root disk costs to make on this filesystem: a full copy, or
# a reflink when the filesystem shares blocks (btrfs, xfs with reflink). Not root.
source "$(dirname "$0")/lib.sh"
header
src="$M3_WORK/disk-src.img"
[ -f "$src" ] || { say "writing a 1 GiB test image"; dd if=/dev/urandom of="$src" bs=1M count=1024 status=none; sync; }
record "disk: filesystem under $M3_WORK" "$(stat -f -c %T "$M3_WORK")"
for mode in always never; do
  rm -f "$M3_WORK/disk-copy.img"; sync
  t0=$(now_ms)
  if cp --reflink="$mode" "$src" "$M3_WORK/disk-copy.img" 2>/dev/null; then
    sync; record "disk: 1 GiB copy, reflink=$mode (ms)" "$(( $(now_ms) - t0 ))"
  else
    record "disk: 1 GiB copy, reflink=$mode" "not supported here"
  fi
done
rm -f "$M3_WORK/disk-copy.img"
