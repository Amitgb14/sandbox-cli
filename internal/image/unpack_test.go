package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name, link, body string
	typ              byte
	mode             int64
}

func layer(t *testing.T, gz bool, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var w *tar.Writer
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(&buf)
		w = tar.NewWriter(zw)
	} else {
		w = tar.NewWriter(&buf)
	}
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body))}
		if h.Mode == 0 {
			h.Mode = 0o644
			if e.typ == tar.TypeDir {
				h.Mode = 0o755
			}
		}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			w.Write([]byte(e.body))
		}
	}
	w.Close()
	if zw != nil {
		zw.Close()
	}
	return &buf
}

func dir(n string) entry          { return entry{name: n, typ: tar.TypeDir} }
func file(n, body string) entry   { return entry{name: n, body: body, typ: tar.TypeReg} }
func symlink(n, to string) entry  { return entry{name: n, link: to, typ: tar.TypeSymlink} }
func hardlink(n, to string) entry { return entry{name: n, link: to, typ: tar.TypeLink} }

func unpack(t *testing.T, root string, l *bytes.Buffer) error {
	t.Helper()
	var st UnpackStats
	return Unpack(root, l, 1<<30, &st)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func TestUnpackBasics(t *testing.T) {
	root := t.TempDir()
	for _, gz := range []bool{false, true} {
		err := unpack(t, root, layer(t, gz,
			dir("etc"), file("etc/hostname", "box\n"),
			symlink("bin", "usr/bin"), dir("usr/bin"), file("usr/bin/tool", "#!"),
			hardlink("etc/hostname2", "etc/hostname"),
			entry{name: "dev/null", typ: tar.TypeChar},
		))
		if err != nil {
			t.Fatalf("gz=%v: %v", gz, err)
		}
	}
	if read(t, filepath.Join(root, "etc/hostname2")) != "box\n" {
		t.Error("hard link not created")
	}
	// Writing through the usrmerge link lands in usr/bin, inside the root.
	if err := unpack(t, root, layer(t, false, file("bin/other", "x"))); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "usr/bin/other")) != "x" {
		t.Error("a path through an in-root symlink did not resolve within the root")
	}
	if _, err := os.Lstat(filepath.Join(root, "dev/null")); err == nil {
		t.Error("a device node was created")
	}
}

// The attacks: each tries to make the host write outside root, and each must
// fail with nothing outside root touched.
func TestUnpackCannotEscapeTheRoot(t *testing.T) {
	cases := map[string][]entry{
		"dotdot in the name":       {file("../escape", "x")},
		"dotdot mid-name":          {file("a/../../escape", "x")},
		"absolute symlink parent":  {symlink("evil", "OUTSIDE"), file("evil/escape", "x")},
		"relative symlink parent":  {symlink("evil", "../../../../../../OUTSIDE"), file("evil/escape", "x")},
		"symlink chain":            {symlink("a", "b"), symlink("b", "OUTSIDE"), file("a/escape", "x")},
		"symlink then file at it":  {symlink("escape-link", "OUTSIDE/escape"), file("escape-link", "x")},
		"hardlink to outside":      {hardlink("h", "OUTSIDE/victim")},
		"hardlink via symlink dir": {symlink("d", "OUTSIDE"), hardlink("h", "d/victim")},
		"whiteout via symlink dir": {symlink("d", "OUTSIDE"), file("d/.wh.victim", "")},
		"opaque via symlink dir":   {symlink("d", "OUTSIDE"), file("d/.wh..wh..opq", "")},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			outside := t.TempDir()
			victim := filepath.Join(outside, "victim")
			os.WriteFile(victim, []byte("original"), 0o644)
			root := t.TempDir()
			// Substitute the real outside path into link targets.
			var es []entry
			for _, e := range entries {
				e.link = strings.ReplaceAll(e.link, "OUTSIDE", outside)
				es = append(es, e)
			}
			_ = unpack(t, root, layer(t, false, es...)) // may fail or succeed — what matters is below

			if got := read(t, victim); got != "original" {
				t.Fatalf("the victim outside the root was changed or removed: %q", got)
			}
			if _, err := os.Stat(filepath.Join(outside, "escape")); err == nil {
				t.Fatal("a file was written outside the root")
			}
			des, _ := os.ReadDir(outside)
			if len(des) != 1 {
				t.Fatalf("outside the root now holds %d entries", len(des))
			}
		})
	}
}

// An absolute or climbing link target is still resolved within root, so a later
// entry written through it lands inside root — the guest's view of the path.
func TestAbsoluteSymlinkResolvesWithinRoot(t *testing.T) {
	root := t.TempDir()
	err := unpack(t, root, layer(t, false,
		dir("real"), symlink("abs", "/real"), symlink("climb", "../../../real"),
		file("abs/a", "1"), file("climb/b", "2"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "real/a")) != "1" || read(t, filepath.Join(root, "real/b")) != "2" {
		t.Fatal("links were not resolved within the root")
	}
}

func TestWhiteouts(t *testing.T) {
	root := t.TempDir()
	if err := unpack(t, root, layer(t, false,
		dir("keep"), file("keep/a", "a"), file("keep/b", "b"),
		dir("opq"), file("opq/old", "old"),
	)); err != nil {
		t.Fatal(err)
	}
	if err := unpack(t, root, layer(t, false,
		file("keep/.wh.a", ""),
		file("opq/.wh..wh..opq", ""), file("opq/new", "new"),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "keep/a")); err == nil {
		t.Error("whiteout did not remove keep/a")
	}
	if read(t, filepath.Join(root, "keep/b")) != "b" {
		t.Error("whiteout removed a sibling")
	}
	if _, err := os.Lstat(filepath.Join(root, "opq/old")); err == nil {
		t.Error("opaque whiteout kept a lower entry")
	}
	if read(t, filepath.Join(root, "opq/new")) != "new" {
		t.Error("opaque whiteout removed the layer's own entry")
	}
}

func TestUnpackEnforcesTheSizeLimit(t *testing.T) {
	root := t.TempDir()
	var st UnpackStats
	err := Unpack(root, layer(t, false, file("big", strings.Repeat("x", 1000))), 999, &st)
	if err == nil {
		t.Fatal("a layer over the byte limit was accepted")
	}
}

// A symlink loop is an error, not a hang.
func TestSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	err := unpack(t, root, layer(t, false, symlink("a", "b"), symlink("b", "a"), file("a/x", "x")))
	if err == nil || !strings.Contains(err.Error(), "levels of symbolic links") {
		t.Fatalf("err = %v", err)
	}
}
