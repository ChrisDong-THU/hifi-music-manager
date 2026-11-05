//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMinimProgramCopyDoesNotCopyAnIndexOrPrivateData(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "minimserver")
	for _, path := range []string{"data", "tmp", "program/bin"} {
		if err := os.MkdirAll(filepath.Join(source, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{".build-time": "vendor stamp", "program/bin/startd": "#!/bin/sh\nexit 0\n", "data/music-1.mcache": "old index", "data/minimserver.config": "private settings"} {
		if err := os.WriteFile(filepath.Join(source, path), []byte(value), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("program/bin", filepath.Join(source, "bin")); err != nil {
		t.Fatal(err)
	}
	program, err := prepareMinimProgram(home)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(program) != "minimserver" {
		t.Fatal("vendor launcher requires an installation directory named minimserver")
	}
	if _, err := os.Stat(filepath.Join(program, "bin/startd")); err != nil {
		t.Fatal("runtime program links are broken", err)
	}
	if _, err := os.Stat(filepath.Join(program, "data")); !os.IsNotExist(err) {
		t.Fatal("copied private data/index into the host runtime")
	}
	if same, err := prepareMinimProgram(home); err != nil || same != program {
		t.Fatal("program preparation is not idempotent", err)
	}
}

func TestMinimRegistrationInvalidationNeverRemovesItsTarget(t *testing.T) {
	registry, target := t.TempDir(), t.TempDir()
	id := "12345678-1234-1234-1234-123456789012"
	protected := filepath.Join(target, "manager.db")
	if err := os.WriteFile(protected, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(registry, id)); err != nil {
		t.Fatal(err)
	}
	if err := clearMinimRegistration(registry, id); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(protected); err != nil || string(content) != "preserve" {
		t.Fatal("registry cleanup followed a link")
	}
	if err := clearMinimRegistration(registry, "../../escape"); err == nil {
		t.Fatal("invalid instance identity accepted")
	}
}
