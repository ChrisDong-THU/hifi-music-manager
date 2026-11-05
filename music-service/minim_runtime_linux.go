//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	library "hifi.local/library"
)

func runMinimRuntime(home string, cfg serviceConfig) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	var state LibraryState
	var drive USBDrive
	for {
		var err error
		state, err = library.ReadState(home)
		if err == nil && state.ValidateSelection() == nil {
			drive = USBDrive{Root: state.Mount, Device: state.Device, MountID: state.MountID, Writable: true}
			if driveMounted(drive) {
				root, e := os.OpenRoot(drive.Root)
				if e == nil {
					id, e := volumeIDFromRoot(root)
					root.Close()
					if e == nil && id == state.Volume {
						break
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	program, err := prepareMinimProgram(home)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(drive.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	if !driveMounted(drive) {
		return errOffline
	}
	mountID, err := library.DirectoryMountID(root)
	if err != nil || mountID != drive.MountID {
		return errOffline
	}
	volumeID, err := volumeIDFromRoot(root)
	if err != nil || volumeID != state.Volume {
		return errOffline
	}
	relative := filepath.Join(library.DataDirectory, library.MinimServerDirectory, strings.TrimPrefix(cfg.BrowseUDN, "uuid:"))
	if err := seedMinimData(home, root, relative); err != nil {
		return err
	}
	anchor, err := root.Open(".")
	if err != nil {
		return err
	}
	defer anchor.Close()
	link := filepath.Join(program, "data")
	if info, e := os.Lstat(link); e == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("runtime data entry must be a symlink")
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Keep the mounted filesystem handle alive for the entire vendor process.
	if err := os.Symlink(fmt.Sprintf("/proc/%d/fd/%d/%s", os.Getpid(), anchor.Fd(), relative), link); err != nil {
		return err
	}
	if !driveMounted(drive) {
		return errOffline
	}
	command := exec.Command(filepath.Join(program, "bin", "startd"), "wait")
	command.Dir = program
	// Vendor registration is a per-UDN symlink in /tmp, independent of
	// java.io.tmpdir. Invalidate only this owner's instance before retargeting.
	backend, _ := url.Parse(cfg.BackendDescription)
	nativeID := strings.Split(strings.TrimPrefix(backend.Path, "/"), "/")[0]
	if err := clearMinimRegistration("/tmp/.minimserver", nativeID); err != nil {
		return err
	}
	defer clearMinimRegistration("/tmp/.minimserver", nativeID)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if driveMounted(drive) {
				continue
			}
			log.Print("音乐盘已离线，停止 MinimServer")
		case <-ctx.Done():
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return nil
	}
}

func clearMinimRegistration(directory, id string) error {
	if len(id) != 36 || len(strings.ReplaceAll(id, "-", "")) != 32 {
		return errors.New("invalid native identity")
	}
	for _, c := range strings.ReplaceAll(id, "-", "") {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return errors.New("invalid native identity")
		}
	}
	parent, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid native temporary registry")
	}
	path := filepath.Join(directory, id)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSymlink == 0 {
		return errors.New("refusing to replace an unrelated native registration")
	}
	return os.Remove(path) // Unlink the cache entry; never follow or remove its target.
}
