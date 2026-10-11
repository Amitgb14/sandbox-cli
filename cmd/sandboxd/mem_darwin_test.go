//go:build darwin

package main

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// hostMemMB agrees with sysctl(8). Sysctl drops the value's top byte with what
// it takes for a trailing NUL; read without padding it back, this was off.
func TestHostMemMBMatchesSysctl(t *testing.T) {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		t.Skip("no sysctl:", err)
	}
	b, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hostMemMB(), int(b/(1<<20)); got != want {
		t.Fatalf("hostMemMB() = %d, sysctl says %d MiB", got, want)
	}
}
