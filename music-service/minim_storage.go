package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Create one checked component at a time, before any writes can follow a link.
func storageDirectories(root *os.Root, path string) error {
	current := ""
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid storage directory")
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(current, 0700); err != nil {
				return err
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("storage directory cannot be a symlink")
		}
	}
	return nil
}

// A generated program snapshot never contains the vendor data/index. Replacing
// the complete snapshot also drops obsolete jars after a vendor update.
func prepareMinimProgram(home string) (string, error) {
	base, err := os.OpenRoot(home)
	if err != nil {
		return "", err
	}
	defer base.Close()
	info, err := base.Lstat("minimserver")
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid vendor directory")
	}
	source, err := base.OpenRoot("minimserver")
	if err != nil {
		return "", err
	}
	defer source.Close()
	stamp, err := readProgramStamp(source, ".build-time")
	if err != nil {
		return "", err
	}
	if len(stamp) == 0 || len(stamp) > 4096 {
		return "", errors.New("invalid vendor program stamp")
	}
	stamp = append([]byte("program-only\n"), stamp...)
	if err := storageDirectories(base, "runtime/usb-player"); err != nil {
		return "", err
	}
	runtime, err := base.OpenRoot("runtime/usb-player")
	if err != nil {
		return "", err
	}
	defer runtime.Close()
	target := filepath.Join(home, "runtime", "usb-player", "minimserver")
	exists := false
	if info, err := runtime.Lstat("minimserver"); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("invalid runtime program directory")
		}
		exists = true
		if data, err := runtime.Lstat("minimserver/data"); err == nil && data.Mode()&os.ModeSymlink == 0 {
			return "", errors.New("runtime data entry must be a symlink")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if previous, err := readProgramStamp(runtime, "minimserver/.source-stamp"); err == nil && string(previous) == string(stamp) {
			if launcher, err := runtime.Stat("minimserver/bin/startd"); err == nil && launcher.Mode().IsRegular() {
				return target, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	stage := "minimserver.next-" + newID()
	if err := runtime.Mkdir(stage, 0700); err != nil {
		return "", err
	}
	defer runtime.RemoveAll(stage) // Exact generated child; RemoveAll does not follow symlinks.
	output, err := runtime.OpenRoot(stage)
	if err != nil {
		return "", err
	}
	err = copyMinimProgram(source, output)
	if err == nil {
		launcher, checkErr := output.Stat("bin/startd")
		if checkErr != nil {
			err = checkErr
		} else if !launcher.Mode().IsRegular() {
			err = errors.New("missing vendor launcher")
		}
	}
	if err == nil {
		err = output.Mkdir("tmp", 0700)
	}
	if err == nil {
		err = output.WriteFile(".source-stamp", stamp, 0600)
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	previous := "minimserver.previous-" + newID()
	if exists {
		if err := runtime.Rename("minimserver", previous); err != nil {
			return "", err
		}
	}
	if err := runtime.Rename(stage, "minimserver"); err != nil {
		if exists {
			err = errors.Join(err, runtime.Rename(previous, "minimserver"))
		}
		return "", err
	}
	if exists {
		if err := runtime.RemoveAll(previous); err != nil {
			return "", err
		}
	}
	return target, nil
}

func readProgramStamp(root *os.Root, path string) ([]byte, error) {
	const limit = 4096 + len("program-only\n")
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, errors.New("invalid vendor program stamp")
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(data) > limit {
		return nil, errors.New("invalid vendor program stamp")
	}
	return data, err
}

func copyMinimProgram(source, output *os.Root) error {
	return fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if path == "data" || path == "tmp" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return output.Mkdir(path, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := source.Readlink(path)
			if err != nil {
				return err
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), link))
			if filepath.IsAbs(link) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
				return errors.New("vendor program link escapes installation")
			}
			return output.Symlink(link, path)
		}
		if !info.Mode().IsRegular() {
			return errors.New("unexpected vendor program file")
		}
		input, err := source.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		actual, err := input.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, actual) {
			return errors.New("vendor program changed during copy")
		}
		file, err := output.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(file, input)
		return errors.Join(err, file.Close())
	})
}

// Seed only initial settings. Each file is committed after its complete write;
// existing per-disk settings, identity and native cache are never overwritten.
func seedMinimData(home string, root *os.Root, relative string) error {
	if err := storageDirectories(root, relative); err != nil {
		return err
	}
	source, err := os.OpenRoot(filepath.Join(home, "minimserver", "data"))
	if err != nil {
		return err
	}
	defer source.Close()
	dir, err := source.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != "default.profile" && name != "minimserver.config" && !strings.HasSuffix(name, ".license") {
			continue
		}
		if !entry.Type().IsRegular() {
			return errors.New("vendor configuration must be a regular file")
		}
		dest := filepath.Join(relative, name)
		if info, err := root.Lstat(dest); err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("invalid per-disk vendor configuration")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		input, err := source.Open(name)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(input, (2<<20)+1))
		input.Close()
		if err != nil {
			return err
		}
		if len(data) > 2<<20 {
			return errors.New("oversized vendor configuration")
		}
		if err := seedMinimFile(root, dest, data); err != nil {
			return err
		}
	}
	return nil
}

func seedMinimFile(root *os.Root, dest string, data []byte) error {
	temp := filepath.Join(filepath.Dir(dest), ".seed-"+newID())
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if _, err := root.Lstat(dest); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return root.Rename(temp, dest)
}
