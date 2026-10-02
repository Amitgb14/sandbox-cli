//go:build !unix

package main

// withPrivateUmask has nothing to narrow where there is no umask. sandboxd runs
// on macOS and Linux; this exists so the tree still compiles for the platforms
// the CLI ships to as a client.
func withPrivateUmask(fn func()) { fn() }
