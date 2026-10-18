package main

import (
	"strconv"
	"strings"
	"testing"
)

const procDevices = `Character devices:
  1 mem
  4 /dev/vc/0
195 nvidia-frontend
226 drm
510 nvidia-uvm

Block devices:
195 not-a-char
254 virtblk
`

func TestNvidiaNodes(t *testing.T) {
	m := charMajors(strings.NewReader(procDevices))
	if m["nvidia-frontend"] != 195 || m["nvidia-uvm"] != 510 || m["virtblk"] != 0 {
		t.Fatalf("majors: %v", m)
	}
	var got []string
	for _, n := range nvidiaNodes(m, 1, false) {
		got = append(got, n.path+" "+itoa(n.major)+":"+itoa(n.minor))
	}
	want := "/dev/nvidia0 195:0,/dev/nvidiactl 195:255,/dev/nvidia-uvm 510:0,/dev/nvidia-uvm-tools 510:1"
	if strings.Join(got, ",") != want {
		t.Fatalf("nodes:\n%s\nwant:\n%s", strings.Join(got, ","), want)
	}
	// An older driver's name for its major, and modeset loaded.
	old := map[string]uint32{"nvidia": 195}
	if n := nvidiaNodes(old, 2, true); len(n) != 4 || n[3].path != "/dev/nvidia-modeset" || n[3].minor != 254 {
		t.Fatalf("older driver: %+v", n)
	}
	if n := nvidiaNodes(map[string]uint32{}, 1, true); len(n) != 0 {
		t.Fatalf("no driver: %+v", n)
	}
}

func TestMkdev(t *testing.T) {
	// glibc's makedev for the numbers nvidia-modprobe uses, and one past the
	// 8-bit minor and 12-bit major.
	for _, c := range []struct {
		major, minor uint32
		want         uint64
	}{{195, 255, 0xc3ff}, {195, 0, 0xc300}, {510, 1, 0x1fe01}, {4096, 256, 0x100000100000}} {
		if got := mkdev(c.major, c.minor); got != c.want {
			t.Errorf("mkdev(%d,%d) = %#x, want %#x", c.major, c.minor, got, c.want)
		}
	}
}

func itoa(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
