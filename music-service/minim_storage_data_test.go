package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMinimRuntimeKeepsVendorIdentityAndSeparatesDiskCaches(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "minimserver", "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"default.profile": "contentDir=/stable/music", "minimserver.config": "identity=stable", "music-1.mcache": "old host cache"} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	diskA, diskB := t.TempDir(), t.TempDir()
	for i, path := range []string{diskA, diskB, diskA} {
		root, err := os.OpenRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := seedMinimData(home, root, "hifi-music-nano/minimserver/device"); err != nil {
			root.Close()
			t.Fatal(err)
		}
		cache := "hifi-music-nano/minimserver/device/music-1.mcache"
		if i == 0 {
			if err := root.WriteFile(cache, []byte("A cache"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if i == 1 {
			if _, err := root.Stat(cache); !os.IsNotExist(err) {
				t.Fatal("A cache leaked into B")
			}
		}
		if i == 2 {
			content, err := root.ReadFile(cache)
			if err != nil || string(content) != "A cache" {
				t.Fatal("A cache lost")
			}
		}
		root.Close()
	}
	content, err := os.ReadFile(filepath.Join(data, "music-1.mcache"))
	if err != nil || string(content) != "old host cache" {
		t.Fatal("original vendor data changed")
	}
}
