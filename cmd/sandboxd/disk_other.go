//go:build !(linux || darwin)

package main

// diskTotalMB is unknown here; the operator sets --capacity-disk-mb.
func diskTotalMB(string) int { return 0 }
