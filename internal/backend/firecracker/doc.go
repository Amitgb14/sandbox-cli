// Package firecracker is the Linux backend, for self-hosted and cloud: every
// sandbox is a Firecracker microVM.
//
// A sandbox is three disks' worth of state and one process:
//   - the image's root disk, built once per image and shared read-only by every
//     sandbox made from it (internal/image);
//   - a sparse scratch disk of the sandbox's own, which the guest agent overlays
//     on the root — so creating a sandbox costs an empty file, not a copy;
//   - the VMM process, whose vsock bridge is the only way in: every operation is
//     a guestproto request to sandbox-guestd (PID 1 in the guest).
//
// The kernel command line is a measured decision (rewrite M3): with the serial
// console quiet and the keyboard-controller probe off, the guest's init is
// ready ~53 ms after the VMM starts, against ~740 ms with the defaults.
//
// Networking, when enabled, is enforced on the host (network.go); without it a
// guest has no network interface at all, and the backend says so through its
// capabilities so that a request for egress is refused rather than ignored.
package firecracker
