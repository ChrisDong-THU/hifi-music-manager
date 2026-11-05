package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vendorFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, path := range []string{"minimserver/bin", "minimserver/data"} {
		if err := os.MkdirAll(filepath.Join(home, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{".build-time": "original", "bin/startd": "launcher", "obsolete.jar": "old", "data/manager.db": "private"} {
		if err := os.WriteFile(filepath.Join(home, "minimserver", name), []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestProgramSnapshotReplacesObsoleteFilesAndPreservesSource(t *testing.T) {
	home := vendorFixture(t)
	program, err := prepareMinimProgram(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(program, "data")); !os.IsNotExist(err) {
		t.Fatal("private data was copied")
	}
	if err := os.WriteFile(filepath.Join(home, "minimserver/.build-time"), []byte(strings.Repeat("x", 8192)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMinimProgram(home); err == nil {
		t.Fatal("oversized vendor stamp accepted")
	}
	if value, err := os.ReadFile(filepath.Join(program, "bin/startd")); err != nil || string(value) != "launcher" {
		t.Fatal("invalid stamp damaged previous snapshot")
	}
	if err := os.Remove(filepath.Join(home, "minimserver/obsolete.jar")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "minimserver/.build-time"), []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMinimProgram(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(program, "obsolete.jar")); !os.IsNotExist(err) {
		t.Fatal("obsolete program survived update")
	}
	if value, err := os.ReadFile(filepath.Join(home, "minimserver/data/manager.db")); err != nil || string(value) != "private" {
		t.Fatal("vendor private data changed")
	}
}

func TestFailedProgramCopyKeepsPreviousSnapshotAndCleansStage(t *testing.T) {
	home := vendorFixture(t)
	program, err := prepareMinimProgram(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "minimserver/.build-time"), []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	// Missing launcher must never publish a broken snapshot.
	if err := os.Remove(filepath.Join(home, "minimserver/bin/startd")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMinimProgram(home); err == nil {
		t.Fatal("incomplete vendor program accepted")
	}
	if value, err := os.ReadFile(filepath.Join(program, "bin/startd")); err != nil || string(value) != "launcher" {
		t.Fatal("previous snapshot was damaged")
	}
	entries, err := os.ReadDir(filepath.Dir(program))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "minimserver.next-") {
			t.Fatal("failed stage was retained")
		}
	}
}

func TestSnapshotRefusesPrivateDataInGeneratedProgramDirectory(t *testing.T) {
	home := vendorFixture(t)
	program, err := prepareMinimProgram(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(program, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(program, "data/keep")
	if err := os.WriteFile(protected, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMinimProgram(home); err == nil {
		t.Fatal("unexpected runtime private data accepted")
	}
	if value, err := os.ReadFile(protected); err != nil || string(value) != "keep" {
		t.Fatal("unexpected private data removed")
	}
}

func TestMinimSeedPreservesExistingSettingsAndRejectsNonRegularTargets(t *testing.T) {
	home := vendorFixture(t)
	if err := os.WriteFile(filepath.Join(home, "minimserver/data/minimserver.config"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const relative = "hifi-music-nano/minimserver/device"
	if err := seedMinimData(home, root, relative); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(relative+"/minimserver.config", []byte("per-disk settings"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := seedMinimData(home, root, relative); err != nil {
		t.Fatal(err)
	}
	if value, err := root.ReadFile(relative + "/minimserver.config"); err != nil || string(value) != "per-disk settings" {
		t.Fatal("existing settings overwritten")
	}
	if err := root.Remove(relative + "/minimserver.config"); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir(relative+"/minimserver.config", 0700); err != nil {
		t.Fatal(err)
	}
	if err := seedMinimData(home, root, relative); err == nil {
		t.Fatal("configuration directory accepted as a file")
	}
}
