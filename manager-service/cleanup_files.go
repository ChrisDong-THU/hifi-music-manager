package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	library "hifi.local/library"
)

func hexID(s string, length int) bool {
	if len(s) != length || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Resolve each directory through its own handle; never traverse a symlink,
// junction or nonregular file. Only the current disk's guarded roots are used.
func walkCleanup(root *os.Root, dir string, depth int, check func() error, visit func(*os.Root, string, fs.FileInfo) error, empty bool) error {
	if err := check(); err != nil {
		return err
	}
	if depth > 128 {
		return errors.New("清理目录层级过深")
	}
	info, err := root.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("清理目录不是普通目录")
	}
	if err := cleanupDirectoryPath(root, dir); err != nil {
		return err
	}
	child, err := root.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer child.Close()
	f, err := child.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	var issues error
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if err := check(); err != nil {
				return errors.Join(issues, err)
			}
			info, err := child.Lstat(entry.Name())
			if err != nil {
				issues = errors.Join(issues, err)
				continue
			}
			if info.IsDir() {
				err = walkCleanup(child, entry.Name(), depth+1, check, visit, empty)
				// Remove only empty directories; live files keep their parents.
				if err == nil && empty {
					_ = child.Remove(entry.Name())
				}
			} else if info.Mode().IsRegular() {
				err = visit(child, entry.Name(), info)
			} else {
				err = errors.New("清理跳过链接或特殊文件")
			}
			issues = errors.Join(issues, err)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				issues = errors.Join(issues, readErr)
			}
			return issues
		}
	}
}

func removeCleanupFile(root *os.Root, file string, check func() error) error {
	if err := check(); err != nil {
		return err
	}
	if err := regular(root, file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	err := root.Remove(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func removeEmptyParents(root *os.Root, dir, floor string) {
	for dir != "." && (floor == "." || dir == floor || strings.HasPrefix(dir, floor+"/")) {
		if root.Remove(dir) != nil || dir == floor {
			return
		}
		dir = path.Dir(dir)
	}
}

func cleanupDirectoryPath(root *os.Root, dir string) error {
	prefix := ""
	for _, part := range strings.Split(dir, "/") {
		prefix = path.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("清理目录包含链接或特殊文件")
		}
	}
	return nil
}

func cleanupDirectories(root *os.Root, dir string, each func(string, fs.FileInfo) error) error {
	if err := cleanupDirectoryPath(root, dir); err != nil {
		return err
	}
	f, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	var issues error
	for _, entry := range entries {
		info, err := root.Lstat(path.Join(dir, entry.Name()))
		if err == nil {
			err = each(entry.Name(), info)
		}
		issues = errors.Join(issues, err)
	}
	return issues
}

func uploadTemporary(name string) bool {
	_, suffix, ok := strings.Cut(name, ".upload-")
	return ok && hexID(suffix, 32)
}

func musicTemporary(name string) bool {
	if base, nonce, ok := strings.Cut(name, ".tag-"); ok {
		return hexID(nonce, 32) && musicTemporary(base)
	}
	if strings.HasPrefix(name, ".hifi-case-") {
		return hexID(strings.TrimPrefix(name, ".hifi-case-"), 32)
	}
	for _, prefix := range []string{".hifi-edit-", ".hifi-import-"} {
		if strings.HasPrefix(name, prefix) {
			return hexID(strings.TrimSuffix(strings.TrimPrefix(name, prefix), path.Ext(name)), 32)
		}
	}
	return false
}

func pruneCaseProbes(root *os.Root, cutoff time.Time, check func() error) error {
	return cleanupDirectories(root, ".", func(name string, info fs.FileInfo) error {
		if info.Mode().IsRegular() && strings.HasPrefix(name, ".hifi-case-") && hexID(strings.TrimPrefix(name, ".hifi-case-"), 32) && !info.ModTime().After(cutoff) {
			return removeCleanupFile(root, name, check)
		}
		return nil
	})
}

func pruneTemporaryFiles(root *os.Root, dir string, cutoff time.Time, music bool, check func() error) error {
	return walkCleanup(root, dir, 0, check, func(r *os.Root, name string, info fs.FileInfo) error {
		if !info.ModTime().After(cutoff) && ((music && musicTemporary(name)) || (!music && uploadTemporary(name))) {
			return r.Remove(name)
		}
		return nil
	}, false)
}

func pruneUploads(root *os.Root, protected map[string]bool, cutoff time.Time, check func() error, key func(string) string) error {
	return cleanupDirectories(root, "uploads", func(owner string, info fs.FileInfo) error {
		if !hexID(owner, 32) || !info.IsDir() {
			return nil
		}
		// Relative paths from per-directory handles are mapped back to the
		// uploads namespace without traversing arbitrary database paths.
		p := path.Join("uploads", owner)
		err := pruneUploadDirectory(root, p, protected, cutoff, check, 0, key)
		if err == nil {
			_ = root.Remove(p)
		}
		return err
	})
}

func pruneUploadDirectory(root *os.Root, dir string, protected map[string]bool, cutoff time.Time, check func() error, depth int, key func(string) string) error {
	if err := check(); err != nil {
		return err
	}
	if depth > 128 {
		return errors.New("上传目录层级过深")
	}
	return cleanupDirectories(root, dir, func(name string, info fs.FileInfo) error {
		p := path.Join(dir, name)
		if info.IsDir() {
			err := pruneUploadDirectory(root, p, protected, cutoff, check, depth+1, key)
			if err == nil {
				_ = root.Remove(p)
			}
			return err
		}
		if info.Mode().IsRegular() && !protected[key(p)] && !info.ModTime().After(cutoff) &&
			(uploadTemporary(name) || audioExtensions[strings.ToLower(path.Ext(name))] || draftArtworkFile(dir, name)) {
			return removeCleanupFile(root, p, check)
		}
		return nil
	})
}

func draftArtworkFile(dir, name string) bool {
	if path.Base(path.Dir(dir)) != ".artwork" || !hexID(path.Base(dir), 32) {
		return false
	}
	if hexID(name, 64) {
		return true
	}
	hash, nonce, ok := strings.Cut(name, ".pending-")
	return ok && hexID(hash, 64) && hexID(nonce, 32)
}

func pruneArtistVersions(root *os.Root, cutoff time.Time, check func() error) error {
	return cleanupDirectories(root, ".", func(id string, info fs.FileInfo) error {
		if info.Mode().IsRegular() && strings.HasPrefix(id, ".pending-") && hexID(strings.TrimPrefix(id, ".pending-"), 32) && !info.ModTime().After(cutoff) {
			return removeCleanupFile(root, id, check)
		}
		if !hexID(id, 64) || !info.IsDir() {
			return nil
		}
		child, err := root.OpenRoot(id)
		if err != nil {
			return err
		}
		defer child.Close()
		// LoadArtist needs the name to verify identity. Decode the bounded
		// profile first, then use the shared validator before deleting anything.
		profile, err := readCleanupArtist(root, id)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return cleanupDirectories(child, ".", func(name string, info fs.FileInfo) error {
			if !info.Mode().IsRegular() {
				return nil
			}
			hash := strings.TrimSuffix(name, path.Ext(name))
			version := hexID(hash, 64) && (path.Ext(name) == ".jpg" || path.Ext(name) == ".png") && hash != profile.ImageHash
			pending := strings.HasPrefix(name, ".pending-") && hexID(strings.TrimPrefix(name, ".pending-"), 32)
			if (version || pending) && !info.ModTime().After(cutoff) {
				return removeCleanupFile(child, name, check)
			}
			return nil
		})
	})
}

func readCleanupArtist(root *os.Root, id string) (library.Artist, error) {
	var profile library.Artist
	p := path.Join(id, "profile.json")
	if err := regular(root, p); err != nil {
		return profile, err
	}
	f, err := root.Open(p)
	if err != nil {
		return profile, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	if err != nil {
		return profile, err
	}
	if len(data) > 32<<10 {
		return profile, errors.New("歌手资料过大")
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		return profile, err
	}
	if library.ArtistID(profile.Name) != id {
		return profile, errors.New("歌手资料标识不匹配")
	}
	if profile.ImageHash != "" && (!hexID(profile.ImageHash, 64) || (profile.ImageType != "image/png" && profile.ImageType != "image/jpeg")) {
		return profile, errors.New("歌手图片引用无效，跳过清理")
	}
	return library.LoadArtist(root, profile.Name)
}
