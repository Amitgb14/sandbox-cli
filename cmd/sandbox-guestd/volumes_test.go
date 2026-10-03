package main

import (
	"reflect"
	"testing"
)

func TestParseVolumes(t *testing.T) {
	got := parseVolumes("vdc:/data:rw,vdd:/sandbox/home/.cache:ro,vdb:/x:rw,vde:rel:rw,vdf:/a/../b:rw,vdg:/:rw,vdh:/y:maybe,junk")
	want := []volumeMount{
		{dev: "/dev/vdc", path: "/data"},
		{dev: "/dev/vdd", path: "/sandbox/home/.cache", readOnly: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if parseVolumes("") != nil {
		t.Error("no volumes should be nil")
	}
}
