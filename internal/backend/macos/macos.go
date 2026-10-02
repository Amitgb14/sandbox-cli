// Package macos is the local backend: each sandbox is a VM run by the native
// macOS `container` runtime (macOS 26+, arm64). Its renderer, BuildArgs, is a
// pure function of the request with a golden test, like every backend's.
//
// Built at M6. Until then it is only this comment.
package macos
