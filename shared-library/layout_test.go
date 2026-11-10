package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshLayoutAndIdentityAreIdempotent(t *testing.T) {
	d := Drive{Root: t.TempDir(), Writable: true}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		t.Fatal(err)
	}
	d.MountID, err = DirectoryMountID(root)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	check := func(Drive) bool { return true }
	id, err := InitializeDisk(d, check)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"library-id", "music", "resources/artists", "manager/uploads"} {
		if _, err := os.Stat(filepath.Join(DataPath(d.Root), filepath.FromSlash(path))); err != nil {
			t.Fatal(path, err)
		}
	}
	if again, err := InitializeDisk(d, check); err != nil || again != id {
		t.Fatal("initialization replaced identity", err)
	}
	if !InspectDisk(d).Initialized {
		t.Fatal("canonical layout not recognized")
	}
}

func TestInitializationDoesNotMergeUnrecognizedProjectData(t *testing.T) {
	d := Drive{Root: t.TempDir(), Writable: true}
	root, _ := os.OpenRoot(d.Root)
	d.MountID, _ = DirectoryMountID(root)
	root.Close()
	if err := os.Mkdir(DataPath(d.Root), 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(DataPath(d.Root), "foreign-file")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeDisk(d, func(Drive) bool { return true }); err == nil {
		t.Fatal("silently merged an unknown layout")
	}
	if raw, _ := os.ReadFile(keep); string(raw) != "keep" {
		t.Fatal("changed existing data")
	}
	if InspectDisk(d).Issue == "" {
		t.Fatal("unknown layout was offered for initialization")
	}
}
