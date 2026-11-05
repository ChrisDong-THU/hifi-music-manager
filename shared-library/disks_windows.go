//go:build windows

package library

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Volume paths keep an index tied to its volume even if its drive letter changes.
func WindowsVolumePath(file *os.File) (string, error) {
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 1)
	if err != nil {
		return "", fmt.Errorf("无法识别数据卷路径: %w", err)
	}
	if n >= uint32(len(buffer)) {
		return "", errors.New("数据卷路径过长")
	}
	return windows.UTF16ToString(buffer[:n]), nil
}

func DirectoryMountID(root *os.Root) (string, error) {
	f, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	path, err := WindowsVolumePath(f)
	if err != nil {
		return "", err
	}
	end := strings.Index(path, `}\`)
	if end < 0 || !strings.HasPrefix(path, `\\?\Volume{`) {
		return "", fmt.Errorf("不是本机数据卷")
	}
	var serial uint32
	if err := windows.GetVolumeInformationByHandle(windows.Handle(f.Fd()), nil, 0, &serial, nil, nil, nil, 0); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%08x", strings.ToLower(path[:end+2]), serial), nil
}

func windowsDrive(root string) (Drive, error) {
	return windowsDriveInfo(root, true)
}

func windowsDriveInfo(root string, capacity bool) (Drive, error) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Drive{}, err
	}
	label, filesystem, device := make([]uint16, 261), make([]uint16, 261), make([]uint16, 261)
	var flags uint32
	if err := windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, &flags, &filesystem[0], uint32(len(filesystem))); err != nil {
		return Drive{}, err
	}
	if err := windows.GetVolumeNameForVolumeMountPoint(p, &device[0], uint32(len(device))); err != nil {
		return Drive{}, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return Drive{}, err
	}
	id, err := DirectoryMountID(dir)
	dir.Close()
	if err != nil {
		return Drive{}, err
	}
	d := Drive{Device: windows.UTF16ToString(device), Root: root, MountID: id,
		Name: windows.UTF16ToString(label), Filesystem: windows.UTF16ToString(filesystem), Writable: flags&windows.FILE_READ_ONLY_VOLUME == 0}
	if d.Name == "" {
		d.Name = filepath.VolumeName(root)
	}
	if capacity {
		if err := windows.GetDiskFreeSpaceEx(p, &d.Free, &d.Size, nil); err != nil {
			return Drive{}, err
		}
	}
	// Check directory access without creating a probe file on someone else's disk.
	h, err := windows.CreateFile(p, windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err == nil {
		windows.CloseHandle(h)
	} else {
		d.Writable = false
	}
	return d, nil
}

func usbVolume(root string) bool {
	p, err := windows.UTF16PtrFromString(`\\.\` + strings.TrimSuffix(root, `\`))
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	// STORAGE_PROPERTY_QUERY: StorageDeviceProperty / PropertyStandardQuery.
	query, descriptor := make([]byte, 12), make([]byte, 4096)
	var n uint32
	const storageQueryProperty = 0x002d1400
	if windows.DeviceIoControl(h, storageQueryProperty, &query[0], uint32(len(query)), &descriptor[0], uint32(len(descriptor)), &n, nil) != nil || n < 32 {
		return false
	}
	const busTypeUSB = 7
	return binary.LittleEndian.Uint32(descriptor[28:32]) == busTypeUSB
}

func ListDrives(ctx context.Context) ([]Drive, error) {
	buffer := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buffer)), &buffer[0])
	if err != nil {
		return nil, fmt.Errorf("无法枚举系统挂载卷: %w", err)
	}
	if n >= uint32(len(buffer)) {
		return nil, errors.New("系统挂载卷列表过长")
	}
	drives := []Drive{}
	for offset := 0; offset < int(n); {
		end := offset
		for end < int(n) && buffer[end] != 0 {
			end++
		}
		root := windows.UTF16ToString(buffer[offset:end])
		offset = end + 1
		if root == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, _ := windows.UTF16PtrFromString(root)
		kind := windows.GetDriveType(p)
		if kind != windows.DRIVE_REMOVABLE && kind != windows.DRIVE_FIXED {
			continue
		}
		if strings.EqualFold(filepath.VolumeName(root), os.Getenv("SystemDrive")) || !usbVolume(root) {
			continue
		}
		if drive, err := windowsDrive(root); err == nil {
			drives = append(drives, drive)
		}
	}
	return drives, nil
}

func Mounted(d Drive) bool {
	// Capacity queries can stall on removable media. A mount guard needs only
	// the live volume identity and write access, not its free-space statistics.
	current, err := windowsDriveInfo(d.Root, false)
	return err == nil && current.Device == d.Device && current.MountID == d.MountID && current.Writable == d.Writable
}
