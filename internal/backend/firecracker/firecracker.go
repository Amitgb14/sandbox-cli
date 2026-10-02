// Package firecracker is the Linux backend, for self-hosted and cloud: each
// sandbox is a Firecracker microVM started under the jailer by sandboxd running
// as a system service, networked through a tap with egress enforced on the host.
// Its renderer, BuildConfig, is a pure function of the request with a golden
// test.
//
// Built at M5. Until then it is only this comment.
package firecracker
