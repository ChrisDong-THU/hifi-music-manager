//go:build linux

package main

import "testing"

func TestVolumeGuardRejectsSystemRootAndUnMountedDirectories(t *testing.T) {
	for _, path := range []string{"/", t.TempDir()} {
		if _, err := volumeGuard(path); err == nil {
			t.Fatalf("not a dedicated mounted volume: %s", path)
		}
	}
	guard, err := volumeGuard("/proc")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard(); err != nil {
		t.Fatal(err)
	}
}

func TestSelectionMountInfoHandlesEscapedPathsAndGeneration(t *testing.T) {
	data := []byte(`24 1 8:1 / /media/music\040set rw - ext4 /dev/sda1 rw
25 1 8:2 / /media/music\011set rw - ext4 /dev/sdb1 rw
26 1 8:3 / /media/music\012set rw - ext4 /dev/sdc1 rw
27 1 8:4 / /media/music\134040set rw - ext4 /dev/sdd1 rw
28 1 8:5 / /media/music\040set rw - ext4 /dev/sde1 rw
`)
	for _, fixture := range []struct{ path, id string }{
		{"/media/music set", "24"},
		{"/media/music set", "28"},
		{"/media/music\tset", "25"},
		{"/media/music\nset", "26"},
		{`/media/music\040set`, "27"},
	} {
		t.Run(fixture.id, func(t *testing.T) {
			if err := selectionMountInfo(data, fixture.path, fixture.id); err != nil {
				t.Fatalf("selected mount rejected: %v", err)
			}
			if err := selectionMountInfo(data, fixture.path, "999"); err == nil {
				t.Fatal("replaced mount generation accepted")
			}
		})
	}
	if err := selectionMountInfo(data, "/missing", ""); err == nil {
		t.Fatal("missing mount accepted")
	}
}
