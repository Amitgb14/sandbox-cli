package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Formatting shared by the commands that print paths and ages.

// shortenHome renders a path under the user's home as ~/…, which keeps output
// readable without hiding which home a store is actually in.
func shortenHome(p string) string {
	if p == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join("~", rel)
	}
	return p
}

// humanAge renders "how long ago" at the resolution someone hunting for lost
// work actually cares about.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
