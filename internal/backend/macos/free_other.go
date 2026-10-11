//go:build !unix

package macos

// freeBytes is unknown here; the macOS backend runs on a Mac.
func freeBytes(string) (int64, error) { return -1, nil }
