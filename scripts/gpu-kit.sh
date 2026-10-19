#!/bin/sh
# Build a GPU kit: what a node needs to give a sandbox an NVIDIA GPU
# (docs/roadmap/task-9-gpu-sandboxes.md). Everything in it is built together,
# because each part must match the others exactly:
#
#   OUT/vmlinux            the guest kernel: Firecracker's CI config for this
#                          kernel, with module support added
#   OUT/rootfs/            what sandboxd adds to a GPU sandbox's root disk, at
#                          these paths under the guest's /:
#     usr/lib/sandbox-gpu/   NVIDIA's open kernel modules, built against that
#                            kernel, and modules.list, their load order (read
#                            by sandbox-guestd)
#     usr/bin, usr/lib/...   NVIDIA's userspace driver of the same version
#                            (nvidia-smi, libcuda, libnvidia-ml, ...), and its
#                            OpenGL, EGL and Vulkan drivers, which the image's
#                            libglvnd and Vulkan loader find through
#     usr/share/glvnd/egl_vendor.d/10_nvidia.json
#     usr/share/vulkan/icd.d/nvidia_icd.json
#     lib/firmware/nvidia/   the GSP firmware the open modules load
#   OUT/kit.json           the versions
#
#   scripts/gpu-kit.sh OUT [WORKDIR]
#
# Every download is checked against the checksum below before it is used.
# Nothing here needs root. The kit holds NVIDIA's files as NVIDIA ships them;
# it is built on the node and never published (NVIDIA's license).
#
# Needs: curl, tar, xz, gcc, make, flex, bison, bc,
# perl, libelf and openssl headers. About 6 GB in WORKDIR while it builds.
set -eu

KERNEL_VERSION=6.1.155
KERNEL_SHA256=c29387aeee085fbcbd91236224b9df805063bac43615e75cea2c6b29604a5c73
# Firecracker's CI config for that kernel, at the release sandboxd runs.
FC_TAG=v1.17.0
FC_CONFIG_SHA256=${FC_CONFIG_SHA256:-}
NVIDIA_VERSION=610.43.02
NVIDIA_RUN_SHA256=3034a054bb4cdf7752ff8dc272564cb105513804bff53538945901b16ca77463

out=${1:?usage: gpu-kit.sh OUT [WORKDIR]}
work=${2:-$(mktemp -d)}
jobs=$(nproc 2>/dev/null || echo 4)
mkdir -p "$out" "$work"
out=$(cd "$out" && pwd)
work=$(cd "$work" && pwd)
log() { echo "gpu-kit: $*" >&2; }
# OUT is replaced at the end, so it must be a kit or nothing.
if [ -n "$(ls -A "$out")" ] && [ ! -f "$out/kit.json" ]; then
  log "$out is not empty and holds no kit.json; give a new or kit directory"
  exit 1
fi

fetch() { # url file sha256
  if [ ! -f "$work/$2" ] || ! echo "$3  $work/$2" | sha256sum -c --quiet - 2>/dev/null; then
    log "downloading $1"
    curl -fsSL --proto '=https' -o "$work/$2.part" "$1"
    mv "$work/$2.part" "$work/$2"
  fi
  if [ -n "$3" ]; then
    echo "$3  $work/$2" | sha256sum -c --quiet - || { log "$2: checksum mismatch"; exit 1; }
  else
    log "$2: no checksum pinned; sha256 $(sha256sum "$work/$2" | cut -d' ' -f1)"
  fi
}

# --- the kernel ------------------------------------------------------------------
fetch "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$KERNEL_VERSION.tar.xz" \
  "linux-$KERNEL_VERSION.tar.xz" "$KERNEL_SHA256"
fetch "https://raw.githubusercontent.com/firecracker-microvm/firecracker/$FC_TAG/resources/guest_configs/microvm-kernel-ci-x86_64-6.1.config" \
  "fc-$FC_TAG-6.1.config" "$FC_CONFIG_SHA256"
ksrc="$work/linux-$KERNEL_VERSION"
if [ ! -d "$ksrc" ]; then
  log "unpacking the kernel"
  tar -C "$work" -xJf "$work/linux-$KERNEL_VERSION.tar.xz"
fi
cp "$work/fc-$FC_TAG-6.1.config" "$ksrc/.config"
# What the CI config leaves out and a GPU driver needs: loadable modules (the
# NVIDIA driver is one), and DRM for nvidia-drm and a GPU desktop later.
# Module signing stays off: the modules are the operator's, loaded by
# sandbox-guestd only, from the kit.
(cd "$ksrc" && ./scripts/config \
  --enable MODULES --enable MODULE_UNLOAD \
  --disable MODULE_SIG --disable MODVERSIONS \
  --enable DRM --enable DRM_KMS_HELPER \
  --set-str LOCALVERSION "-sandbox-gpu" \
  && make -s olddefconfig)
for opt in MODULES PVH VIRTIO_PCI VIRTIO_VSOCKETS DRM; do
  grep -q "^CONFIG_$opt=y" "$ksrc/.config" || { log "the kernel config lost CONFIG_$opt"; exit 1; }
done
log "building the kernel ($jobs jobs)"
# modules too, not only modules_prepare: NVIDIA's build checks against the
# Module.symvers that writes.
make -s -C "$ksrc" -j"$jobs" vmlinux modules
release=$(make -s -C "$ksrc" kernelrelease)

# --- NVIDIA's open kernel modules ------------------------------------------------
fetch "https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/$NVIDIA_VERSION.tar.gz" \
  "open-gpu-kernel-modules-$NVIDIA_VERSION.tar.gz" "${NVIDIA_OPEN_SHA256:-}"
osrc="$work/open-gpu-kernel-modules-$NVIDIA_VERSION"
[ -d "$osrc" ] || tar -C "$work" -xzf "$work/open-gpu-kernel-modules-$NVIDIA_VERSION.tar.gz"
log "building NVIDIA's open kernel modules against $release"
make -s -C "$osrc" -j"$jobs" modules SYSSRC="$ksrc" SYSOUT="$ksrc" >/dev/null

# --- NVIDIA's userspace driver and firmware ---------------------------------------
run="NVIDIA-Linux-x86_64-$NVIDIA_VERSION.run"
fetch "https://download.nvidia.com/XFree86/Linux-x86_64/$NVIDIA_VERSION/$run" "$run" "$NVIDIA_RUN_SHA256"
drv="$work/NVIDIA-Linux-x86_64-$NVIDIA_VERSION"
[ -d "$drv" ] || sh "$work/$run" --extract-only --target "$drv" >/dev/null

# --- the kit ------------------------------------------------------------------------
# Assembled beside OUT and moved into place at the end, so OUT is a whole kit
# or the one before it, never half of one.
new="$out.new"
rm -rf "$new"
root="$new/rootfs"
gpu="$root/usr/lib/sandbox-gpu"
lib="$root/usr/lib/x86_64-linux-gnu"
fw="$root/lib/firmware/nvidia/$NVIDIA_VERSION"
mkdir -p "$gpu" "$lib" "$fw" "$root/usr/bin"
cp "$ksrc/vmlinux" "$new/vmlinux"
# Load order: nvidia first, then uvm (CUDA's memory manager), which needs it.
# The display modules (modeset, drm) wait for the GPU desktop.
modules="nvidia nvidia-uvm"
for m in $modules; do
  cp "$osrc/kernel-open/$m.ko" "$gpu/"
  echo "$m.ko" >> "$gpu/modules.list"
done
cp "$drv/nvidia-smi" "$root/usr/bin/"
# What CUDA and nvidia-smi load, then the graphics drivers: EGL, which a GPU
# desktop draws through (VirtualGL's EGL back end, with no X server on the
# card), GLX and GLES, which glvnd dispatches to, and the cores they share;
# libGLX_nvidia is also the Vulkan driver. Not glvnd itself (libEGL, libGL,
# libGLX): that is the image's, as on any distribution. Each gets its soname
# link, as the installer would make; the guest's dynamic loader searches the
# multiarch directory.
for l in libcuda libnvidia-ml libnvidia-ptxjitcompiler libnvidia-nvvm libnvidia-gpucomp libnvidia-cfg \
         libEGL_nvidia libGLX_nvidia libGLESv2_nvidia libGLESv1_CM_nvidia \
         libnvidia-eglcore libnvidia-glcore libnvidia-glsi libnvidia-tls libnvidia-glvkspirv; do
  f="$drv/$l.so.$NVIDIA_VERSION"
  [ -f "$f" ] || { log "not in the driver: $l.so.$NVIDIA_VERSION (skipped)"; continue; }
  cp "$f" "$lib/"
  soname=$(readelf -d "$f" | sed -n 's/.*SONAME.*\[\(.*\)\]/\1/p')
  # Some name themselves (libnvidia-glcore.so.<version>): no link, or it
  # would replace the file with a link to itself.
  if [ -n "$soname" ] && [ "$soname" != "$l.so.$NVIDIA_VERSION" ]; then
    ln -sf "$l.so.$NVIDIA_VERSION" "$lib/$soname"
  fi
done
# Every library is a file, and every link reaches one.
for f in "$lib"/*; do
  [ -f "$f" ] || { log "$f: a broken link in the kit"; exit 1; }
done
ln -sf libcuda.so.1 "$lib/libcuda.so"
mkdir -p "$root/usr/share/glvnd/egl_vendor.d" "$root/usr/share/vulkan/icd.d"
cp "$drv/10_nvidia.json" "$root/usr/share/glvnd/egl_vendor.d/"
cp "$drv/nvidia_icd.json" "$root/usr/share/vulkan/icd.d/"
cp "$drv"/firmware/*.bin "$fw/"
chmod -R u=rwX,go=rX "$root"

cat > "$new/kit.json" <<EOF
{
  "kit": 1,
  "kernel": "$release",
  "kernel_source": "linux-$KERNEL_VERSION with Firecracker $FC_TAG's CI config, modules enabled",
  "nvidia": "$NVIDIA_VERSION",
  "built": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
rm -rf "$out"
mv "$new" "$out"
log "kit in $out: kernel $release, NVIDIA $NVIDIA_VERSION"
