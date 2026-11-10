package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	DataDirectory    = "hifi-music-nano"
	MusicDirectory   = "music"
	ArtistDirectory  = "resources/artists"
	ManagerDirectory = "manager"
	DatabaseFile     = "manager.db"
	UploadsDirectory = "uploads"
	IdentityFile     = "library-id"
)

type Drive struct {
	Device     string `json:"device"`
	Root       string `json:"root"`
	Filesystem string `json:"filesystem"`
	MountID    string `json:"mount_id"`
	Writable   bool   `json:"writable"`
	Name       string `json:"name"`
	Size       uint64 `json:"size"`
	Free       uint64 `json:"free"`
}
type DiskInfo struct {
	Drive
	DataRoot    string `json:"data_root"`
	Initialized bool   `json:"initialized"`
	VolumeID    string `json:"volume_id,omitempty"`
	Issue       string `json:"issue,omitempty"`
}
type DiskPreference struct {
	VolumeID string `json:"volume_id"`
}

func DataPath(mount string) string    { return filepath.Join(mount, DataDirectory) }
func MusicPath(mount string) string   { return filepath.Join(DataPath(mount), MusicDirectory) }
func ManagerPath(mount string) string { return filepath.Join(DataPath(mount), ManagerDirectory) }

func ValidID(id string) bool {
	if len(id) != 48 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func VolumeID(root *os.Root) (string, error) {
	info, err := root.Lstat(IdentityFile)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 128 {
		return "", errors.New("数据盘标识无效")
	}
	f, err := root.Open(IdentityFile)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if !ValidID(id) {
		return "", errors.New("数据盘标识无效")
	}
	return id, nil
}

func emptyProject(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) != 0 {
		return errors.New("项目目录包含未识别的数据，请清理该项目目录后重新初始化")
	}
	return nil
}

var layoutDirectories = []string{MusicDirectory, ArtistDirectory, ManagerDirectory, ManagerDirectory + "/" + UploadsDirectory}

func InspectDisk(d Drive) DiskInfo {
	info := DiskInfo{Drive: d, DataRoot: DataPath(d.Root)}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		info.Issue = "无法读取挂载目录"
		return info
	}
	defer root.Close()
	if err := directory(root, DataDirectory, false); errors.Is(err, os.ErrNotExist) {
		return info
	} else if err != nil {
		info.Issue = "项目目录不是安全的文件夹"
		return info
	}
	project, err := root.OpenRoot(DataDirectory)
	if err != nil {
		info.Issue = "无法读取项目目录"
		return info
	}
	defer project.Close()
	id, err := VolumeID(project)
	if errors.Is(err, os.ErrNotExist) {
		if e := emptyProject(project); e != nil {
			info.Issue = e.Error()
			return info
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		info.Issue = err.Error()
		return info
	}
	info.VolumeID = id
	for _, path := range layoutDirectories {
		if err := directory(project, filepath.FromSlash(path), false); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				info.Issue = "项目目录结构无效"
			}
			return info
		}
	}
	info.Initialized = id != ""
	return info
}

// Only add the canonical project directories; never erase a disk during selection.
func InitializeDisk(d Drive, mounted func(Drive) bool) (string, error) {
	if !d.Writable || !mounted(d) {
		return "", errors.New("数据盘已离线或只读")
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	actual, err := DirectoryMountID(root)
	if err != nil {
		return "", err
	}
	if actual != d.MountID || !mounted(d) {
		return "", errors.New("数据盘挂载状态已改变")
	}
	if err := directory(root, DataDirectory, true); err != nil {
		return "", err
	}
	project, err := root.OpenRoot(DataDirectory)
	if err != nil {
		return "", err
	}
	defer project.Close()
	if _, err := VolumeID(project); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := emptyProject(project); err != nil {
			return "", err
		}
	}
	for _, path := range layoutDirectories {
		if !mounted(d) {
			return "", errors.New("挂载状态已改变")
		}
		if err := directory(project, filepath.FromSlash(path), true); err != nil {
			return "", err
		}
	}
	if id, err := VolumeID(project); err == nil {
		return id, nil
	}
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(value[:])
	if !mounted(d) {
		return "", errors.New("挂载状态已改变")
	}
	if err := atomic(project, IdentityFile, []byte(id)); err != nil {
		return "", err
	}
	return id, nil
}

func ReadPreference(home string) (DiskPreference, error) {
	var pref DiskPreference
	data, err := os.ReadFile(filepath.Join(home, "config", "data-disk.json"))
	if errors.Is(err, os.ErrNotExist) {
		return pref, nil
	}
	if err != nil {
		return pref, err
	}
	if err := json.Unmarshal(data, &pref); err != nil {
		return pref, err
	}
	if !ValidID(pref.VolumeID) {
		return pref, errors.New("指定数据盘标识无效")
	}
	return pref, nil
}

func WritePreference(home, id string) error {
	if !ValidID(id) {
		return errors.New("数据盘标识无效")
	}
	path := filepath.Join(home, "config")
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	data, _ := json.Marshal(DiskPreference{VolumeID: id})
	return atomic(root, "data-disk.json", data)
}
