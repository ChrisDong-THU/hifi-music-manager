// This checker parses every platform's source; build tags do not hide violations.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/check-architecture.go PROJECT")
		os.Exit(1)
	}
	if err := checkArchitecture(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Package dependency rules passed for all platform sources.")
}

func checkArchitecture(root string) error {
	rules := []struct {
		directory string
		allowed   func(string, bool) bool
	}{
		{"shared-library", func(dependency string, _ bool) bool {
			return !importWithin(dependency, "hifi.local/manager-service") && !strings.HasPrefix(dependency, ".")
		}},
		{"manager-service/internal/audioquality", audioDependencyAllowed},
	}
	for _, rule := range rules {
		count := 0
		err := filepath.WalkDir(filepath.Join(root, rule.directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			count++
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				dependency, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if !rule.allowed(dependency, strings.HasSuffix(path, "_test.go")) {
					relative, _ := filepath.Rel(root, path)
					return fmt.Errorf("%s: dependency %q violates the %s boundary", relative, dependency, rule.directory)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("%s: no Go sources found; architecture check would be empty", rule.directory)
		}
	}
	return nil
}

func importWithin(dependency, prefix string) bool {
	return dependency == prefix || strings.HasPrefix(dependency, prefix+"/")
}

func audioDependencyAllowed(dependency string, test bool) bool {
	if importWithin(dependency, "github.com/mewkiz/flac") {
		return true
	}
	// External-package tests may exercise the public API, but cannot import app code.
	if test && dependency == "hifi.local/manager-service/internal/audioquality" {
		return true
	}
	if strings.Contains(strings.Split(dependency, "/")[0], ".") || dependency == "C" {
		return false
	}
	return !importWithin(dependency, "net") && !importWithin(dependency, "database") && dependency != "os/exec"
}
