package main

import (
	"path"
	"strings"
)

// volumeMount is one entry of sbx.volumes: a drive, where to mount it, and
// whether read-only.
type volumeMount struct {
	dev, path string
	readOnly  bool
}

// parseVolumes reads sbx.volumes ("vdc:/data:rw,vdd:/cache:ro"). The host
// validated these, but this is PID 1: an entry it cannot read as a plain
// absolute path on a virtio disk is dropped rather than acted on.
func parseVolumes(arg string) []volumeMount {
	var out []volumeMount
	if arg == "" {
		return nil
	}
	for _, e := range strings.Split(arg, ",") {
		f := strings.Split(e, ":")
		if len(f) != 3 || len(f[0]) != 3 || !strings.HasPrefix(f[0], "vd") || f[0][2] < 'c' || f[0][2] > 'z' {
			continue
		}
		p := f[1]
		if !strings.HasPrefix(p, "/") || p == "/" || path.Clean(p) != p {
			continue
		}
		if f[2] != "rw" && f[2] != "ro" {
			continue
		}
		out = append(out, volumeMount{dev: "/dev/" + f[0], path: p, readOnly: f[2] == "ro"})
	}
	return out
}
