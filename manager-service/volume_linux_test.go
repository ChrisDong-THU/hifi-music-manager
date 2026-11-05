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
