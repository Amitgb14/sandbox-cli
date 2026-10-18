#!/bin/sh
# Hand a GPU to sandboxes, or give it back to the host
# (docs/roadmap/task-9-gpu-sandboxes.md, phase 0).
#
#   scripts/gpu-vfio.sh status 0000:01:00.0   its IOMMU group, drivers, who has it open
#   scripts/gpu-vfio.sh bind   0000:01:00.0   every device in its group to vfio-pci
#   scripts/gpu-vfio.sh unbind 0000:01:00.0   back to the host's drivers
#
# bind and unbind need root. A GPU is handed over with every other device in
# its IOMMU group (its audio function, usually), because VFIO gives a VM a
# whole group or nothing. Nothing on the host may have the card open: bind
# refuses while anything does, rather than hang in the driver's unbind. The
# change lasts until the next boot.
set -eu

cmd=${1:?usage: gpu-vfio.sh status|bind|unbind BDF}
bdf=${2:?usage: gpu-vfio.sh status|bind|unbind BDF}
case $bdf in
  [0-9a-f][0-9a-f][0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f].[0-7]) ;;
  *) echo "gpu-vfio: $bdf is not a PCI address like 0000:01:00.0" >&2; exit 2 ;;
esac
dev=/sys/bus/pci/devices/$bdf
[ -e "$dev" ] || { echo "gpu-vfio: no PCI device $bdf" >&2; exit 1; }
[ -e "$dev/iommu_group" ] || { echo "gpu-vfio: $bdf has no IOMMU group: is the IOMMU on (amd_iommu / intel_iommu=on)?" >&2; exit 1; }
group=$(basename "$(readlink "$dev/iommu_group")")
members=$(ls "/sys/kernel/iommu_groups/$group/devices")

driver() { [ -e "/sys/bus/pci/devices/$1/driver" ] && basename "$(readlink "/sys/bus/pci/devices/$1/driver")" || echo none; }

# Who has the card open: its NVIDIA nodes, and its DRM nodes.
users() {
  nodes=""
  for n in /dev/nvidia[0-9]* /dev/nvidiactl /dev/nvidia-uvm /dev/nvidia-modeset; do
    [ -e "$n" ] && nodes="$nodes $n"
  done
  for d in "$dev"/drm/card* "$dev"/drm/renderD*; do
    [ -e "$d" ] && nodes="$nodes /dev/dri/$(basename "$d")"
  done
  [ -n "$nodes" ] && fuser -v $nodes 2>&1 | grep -v "^ *USER\|Cannot stat" || true
}

case $cmd in
status)
  echo "IOMMU group $group:"
  for m in $members; do
    printf '  %s  %-14s %s\n' "$m" "$(driver "$m")" "$(lspci -s "$m" 2>/dev/null | cut -d' ' -f2-)"
  done
  echo "open on the host:"
  [ "$(id -u)" = 0 ] || echo "  (only your own processes are visible without root)"
  u=$(users); [ -n "$u" ] && echo "$u" || echo "  nothing"
  [ -e "/dev/vfio/$group" ] && echo "VM-ready: /dev/vfio/$group" || true
  ;;
bind)
  [ "$(id -u)" = 0 ] || { echo "gpu-vfio: bind needs root" >&2; exit 1; }
  u=$(users)
  if [ -n "$u" ]; then
    echo "gpu-vfio: the card is open on the host; stop these first (nvidia-persistenced, a display server, ...):" >&2
    echo "$u" >&2
    exit 1
  fi
  modprobe vfio-pci
  for m in $members; do
    [ "$(driver "$m")" = vfio-pci ] && continue
    echo vfio-pci > "/sys/bus/pci/devices/$m/driver_override"
    [ -e "/sys/bus/pci/devices/$m/driver" ] && echo "$m" > "/sys/bus/pci/devices/$m/driver/unbind"
    echo "$m" > /sys/bus/pci/drivers_probe
    echo "  $m -> $(driver "$m")"
  done
  ls -l "/dev/vfio/$group"
  ;;
unbind)
  [ "$(id -u)" = 0 ] || { echo "gpu-vfio: unbind needs root" >&2; exit 1; }
  for m in $members; do
    echo > "/sys/bus/pci/devices/$m/driver_override"
    [ "$(driver "$m")" = vfio-pci ] && echo "$m" > /sys/bus/pci/drivers/vfio-pci/unbind
    echo "$m" > /sys/bus/pci/drivers_probe
    echo "  $m -> $(driver "$m")"
  done
  ;;
*)
  echo "usage: gpu-vfio.sh status|bind|unbind BDF" >&2; exit 2 ;;
esac
