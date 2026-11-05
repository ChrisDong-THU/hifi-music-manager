//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"

	library "hifi.local/library"
)

func windowsMountID(volume string) (string, error) {
	root, err := os.OpenRoot(volume)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return library.DirectoryMountID(root)
}

func volumeGuard(volume string) (func() error, error) {
	id, err := windowsMountID(volume)
	if err != nil {
		return nil, err
	}
	return func() error { return selectionMount(volume, id) }, nil
}

func selectionMount(volume, id string) error {
	actual, err := windowsMountID(volume)
	if err != nil || actual != id {
		return errors.New("数据盘已离线或盘符对应的数据卷已改变")
	}
	return nil
}

func anchoredDatabase(home string) (string, *os.File, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	anchor, err := root.Open(".")
	if err != nil {
		return "", nil, err
	}
	path, err := library.WindowsVolumePath(anchor)
	if err != nil {
		anchor.Close()
		return "", nil, err
	}
	return filepath.Join(path, library.DatabaseFile), anchor, nil
}
