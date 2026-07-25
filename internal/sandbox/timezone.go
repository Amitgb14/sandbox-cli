package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// A container built from the base image has no idea where its user is: TZ is
// unset and /etc/localtime is UTC. Everything an agent stamps inside it is then
// recorded at +0000 while the same person's work on the host records their real
// offset — most visibly in git, where two commits made minutes apart, one in a
// sandbox and one outside it, sit hours apart in `git log`. The instant is
// right and the offset is wrong, which is the kind of wrong that surfaces long
// after the run.
//
// The zone is forwarded as a *name*, never by mounting the host's /etc/localtime.
// A name is a string; a mount is a host path, and the workspace is the only host
// path this tool reaches for uninvited (see mounts.go). The base image already
// carries tzdata to interpret it, and a name survives a DST boundary mid-session
// where a fixed offset would not.
//
// hostTimezone is a variable so tests can pin it: BuildSpec is expected to
// produce the same spec on every machine, and this is the one input that is
// genuinely different on each.
var hostTimezone = resolveHostTimezone

// resolveHostTimezone reports the host's zone, or "" when it cannot be
// established — in which case nothing is forwarded and the container keeps the
// UTC it has always had. Guessing a zone would be worse than the honest default:
// a wrong offset is indistinguishable from a right one in the output.
func resolveHostTimezone() string {
	// What the user set beats what the system files say — someone running with
	// TZ set has already answered this question, on purpose.
	if tz, ok := os.LookupEnv("TZ"); ok && validZoneName(tz) {
		return tz
	}
	// The symlink both macOS and Linux keep, pointing into a zoneinfo tree:
	// /usr/share/zoneinfo/America/Los_Angeles on Linux,
	// /var/db/timezone/zoneinfo/America/Los_Angeles on macOS. The name is
	// whatever follows the tree root, which is why this cuts on the directory
	// rather than assuming a prefix.
	if dest, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(filepath.ToSlash(dest), "zoneinfo/"); ok && validZoneName(name) {
			return name
		}
	}
	// Debian and friends also write it down in plain text.
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(b)); validZoneName(name) {
			return name
		}
	}
	return ""
}

// validZoneName rejects anything that does not look like a zone name before it
// reaches the argv. This value is read off the host's filesystem and rendered
// into a `docker run -e` argument, so it is checked at the point of use rather
// than trusted for being local: a name is a short run of unsurprising characters
// and everything else is a file that does not mean what we think it does.
func validZoneName(s string) bool {
	if s == "" || len(s) > 64 || strings.HasPrefix(s, "/") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '_', r == '-', r == '+', r == ':', r == '.':
		default:
			return false
		}
	}
	// "." and ".." are paths, not zones, and a name that walks upward is a name
	// we did not read correctly.
	return !strings.Contains(s, "..")
}
