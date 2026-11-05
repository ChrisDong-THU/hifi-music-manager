//go:build windows

package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsDirectoryIdentityBelongsToItsVolume(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	id, err := DirectoryMountID(root)
	if err != nil || id == "" {
		t.Fatal("cannot identify directory volume", err)
	}
	drive, err := windowsDrive(filepath.VolumeName(path) + `\`)
	if err != nil || id != drive.MountID {
		t.Fatal("directory identity differs from its mounted volume", err)
	}
	if !Mounted(drive) {
		t.Fatal("current volume not recognized")
	}
	drive.MountID += "-replaced"
	if Mounted(drive) {
		t.Fatal("accepted a different volume identity")
	}
}

func TestWindowsUSBDiscoveryDoesNotSelectSystemStorage(t *testing.T) {
	drives, err := ListDrives(context.Background())
	if err != nil || drives == nil {
		t.Fatal("USB enumeration failed", err)
	}
	for _, d := range drives {
		if strings.EqualFold(filepath.VolumeName(d.Root), os.Getenv("SystemDrive")) || !filepath.IsAbs(d.Root) || d.MountID == "" || d.Device == "" {
			t.Fatal("invalid USB volume", d)
		}
		t.Logf("USB: %s %s %s writable=%v", d.Root, d.Name, d.Filesystem, d.Writable)
	}
}
