package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaseFoldProbePreservesFilesAndCleansItsTemporaryEntries(t *testing.T) {
	directory := t.TempDir()
	// An explicit project-owned test directory permits the same regression on
	// a target FUSE filesystem. Normal test runs stay in their temporary root.
	if volume := os.Getenv("HIFI_CASEFOLD_TEST_ROOT"); volume != "" {
		var err error
		directory, err = os.MkdirTemp(volume, ".casefold-test-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(directory)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("MixedCase", []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	_, lookupErr := root.Stat("MIXEDCASE")
	if lookupErr != nil && !os.IsNotExist(lookupErr) {
		t.Fatal(lookupErr)
	}
	wantFold := lookupErr == nil
	for i := 0; i < 2; i++ {
		fold, err := detectCaseFold(root)
		if err != nil || fold != wantFold {
			t.Fatal("filesystem lookup semantics were not preserved", fold, wantFold, err)
		}
	}
	if data, err := root.ReadFile("MixedCase"); err != nil || string(data) != "preserve" {
		t.Fatal("existing file was changed")
	}
	entries, err := os.ReadDir(filepath.Clean(directory))
	if err != nil || len(entries) != 1 || entries[0].Name() != "MixedCase" {
		t.Fatal("probe left temporary files", entries, err)
	}
}
