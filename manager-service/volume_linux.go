//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	library "hifi.local/library"
)

var mountInfoPathEscapes = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

// Pin the mount generation. A lost or replaced USB must never redirect writes
// into the system filesystem beneath its old mount point.
func volumeGuard(volume string) (func() error, error) {
	root, err := filepath.EvalSymlinks(volume)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil || root == "/" {
		return nil, errors.New("音乐卷必须是独立挂载点")
	}
	lookup := func() (string, error) {
		raw, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return "", err
		}
		return mountIDAtPath(raw, root)
	}
	id, err := lookup()
	if err != nil {
		return nil, err
	}
	return func() error {
		current, err := lookup()
		if err != nil {
			return err
		}
		if current != id {
			return fmt.Errorf("音乐卷已重新挂载，请重启管理服务后继续")
		}
		return nil
	}, nil
}

func selectionMount(volume, id string) error {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	return selectionMountInfo(data, volume, id)
}

func selectionMountInfo(data []byte, volume, id string) error {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 6 && mountInfoPathEscapes.Replace(fields[4]) == volume && fields[0] == id {
			return nil
		}
	}
	return errors.New("音乐盘挂载代次已改变")
}

// Both selection and the ongoing guard must decode the same mountinfo path.
func mountIDAtPath(data []byte, volume string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 6 && mountInfoPathEscapes.Replace(fields[4]) == volume {
			return fields[0], nil
		}
	}
	return "", errors.New("音乐卷未挂载，已停止文件操作")
}

func anchoredDatabase(home string) (string, *os.File, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("/proc/self/fd/%d/%s", file.Fd(), library.DatabaseFile), file, nil
}
