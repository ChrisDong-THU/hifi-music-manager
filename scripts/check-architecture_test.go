package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func architectureFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeArchitectureSource(t, root, "shared-library/layout.go", "package library\nimport _ \"os\"\n")
	writeArchitectureSource(t, root, "manager-service/internal/audioquality/analysis.go", "package audioquality\nimport _ \"github.com/mewkiz/flac\"\n")
	return root
}

func writeArchitectureSource(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureAllowsExistingDependenciesAndBlackBoxTests(t *testing.T) {
	root := architectureFixture(t)
	writeArchitectureSource(t, root, "manager-service/internal/audioquality/api_test.go", "package audioquality_test\nimport _ \"hifi.local/manager-service/internal/audioquality\"\n")
	if err := checkArchitecture(root); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureChecksExcludedPlatformsAndAliases(t *testing.T) {
	for _, item := range []struct{ directory, dependency string }{
		{"shared-library", "hifi.local/manager-service/internal/audioquality"},
		{"manager-service/internal/audioquality", "net/http"},
		{"manager-service/internal/audioquality", "database/sql"},
		{"manager-service/internal/audioquality", "go.etcd.io/bbolt"},
		{"manager-service/internal/audioquality", "hifi.local/library"},
		{"manager-service/internal/audioquality", "../../../shared-library"},
	} {
		t.Run(item.dependency, func(t *testing.T) {
			root := architectureFixture(t)
			writeArchitectureSource(t, root, item.directory+"/hidden_linux.go", "//go:build never\n\npackage fixture\nimport alias \""+item.dependency+"\"\n")
			if err := checkArchitecture(root); err == nil || !strings.Contains(err.Error(), item.dependency) {
				t.Fatalf("forbidden dependency was not reported: %v", err)
			}
		})
	}
}

func TestArchitectureRejectsMissingOrEmptySourceTrees(t *testing.T) {
	root := t.TempDir()
	if err := checkArchitecture(root); err == nil {
		t.Fatal("missing tree passed")
	}
	if err := os.MkdirAll(filepath.Join(root, "shared-library"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkArchitecture(root); err == nil || !strings.Contains(err.Error(), "no Go sources") {
		t.Fatalf("empty tree passed: %v", err)
	}
}
