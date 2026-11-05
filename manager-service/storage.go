package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	library "hifi.local/library"
)

const uploadsPrefix = library.UploadsDirectory + "/"

// Probe only project-owned, extensionless temporary files. Actual filesystem
// semantics matter here: exFAT is case-insensitive on Linux as well as Windows.
func detectCaseFold(root *os.Root) (bool, error) {
	name := ".hifi-case-" + uid()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	defer root.Remove(name)
	if err := f.Close(); err != nil {
		return false, err
	}
	// Some FUSE drivers synthesize different inode numbers for case aliases.
	// Exclusive creation tests lookup semantics without relying on those IDs.
	upperName := strings.ToUpper(name)
	upper, err := root.OpenFile(upperName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer root.Remove(upperName)
	return false, upper.Close()
}

func storagePathKey(p string, fold bool) string {
	p = filepath.ToSlash(p)
	if fold {
		return strings.ToUpper(p)
	}
	return p
}
func (a *app) dataPathKey(p string) string { return storagePathKey(p, a.dataCaseFold) }

func prepareVolumeDirectories(volume, music, data string, guard func() error) error {
	if err := guard(); err != nil {
		return err
	}
	root, err := os.OpenRoot(volume)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, path := range []string{music, data, filepath.Join(data, library.UploadsDirectory), filepath.Join(filepath.Dir(music), filepath.FromSlash(library.ArtistDirectory))} {
		relative, err := filepath.Rel(volume, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("存储目录越过音乐卷")
		}
		if err := ensureDir(root, relative); err != nil {
			return err
		}
	}
	return guard()
}
