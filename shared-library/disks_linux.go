//go:build linux

package library

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func DirectoryMountID(root *os.Root) (string, error) {
	f, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", f.Fd()))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && key == "mnt_id" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", fmt.Errorf("无法确认数据目录的挂载标识")
}

type mountRecord struct {
	ID, Device, Root, Filesystem string
	Writable                     bool
}

func parseMountTable(data []byte) map[string]mountRecord {
	result := map[string]mountRecord{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		split := -1
		for i, v := range fields {
			if v == "-" {
				split = i
				break
			}
		}
		if len(fields) < 7 || split < 6 || len(fields) < split+4 {
			continue
		}
		root := unescape(fields[4])
		result[root] = mountRecord{ID: fields[0], Device: unescape(fields[split+2]), Root: root, Filesystem: fields[split+1], Writable: !containsOption(fields[5], "ro") && !containsOption(fields[split+3], "ro")}
	}
	return result
}
func containsOption(options, wanted string) bool {
	for _, v := range strings.Split(options, ",") {
		if v == wanted {
			return true
		}
	}
	return false
}
func readMountTable() (map[string]mountRecord, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMountTable(data), nil
}
func ListDrives(ctx context.Context) ([]Drive, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsblk", "-J", "-p", "-o", "NAME,TYPE,TRAN,FSTYPE,LABEL,MOUNTPOINT")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(output) > 2<<20 {
		return nil, fmt.Errorf("USB 设备信息过大")
	}
	mounts, err := readMountTable()
	if err != nil {
		return nil, err
	}
	drives, err := parseUSBDrives(output, mounts)
	if err != nil {
		return nil, err
	}
	for i := range drives {
		drives[i].Writable = drives[i].Writable && syscall.Access(drives[i].Root, 3) == nil
	}
	return drives, nil
}
func parseUSBDrives(data []byte, mounts map[string]mountRecord) ([]Drive, error) {
	type block struct {
		Name       string            `json:"name"`
		Type       string            `json:"type"`
		Transport  string            `json:"tran"`
		Filesystem string            `json:"fstype"`
		Label      string            `json:"label"`
		Mount      *string           `json:"mountpoint"`
		Children   []json.RawMessage `json:"children"`
	}
	var root struct {
		Devices []json.RawMessage `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	out := []Drive{}
	seen := map[string]bool{}
	var walk func(json.RawMessage, string) error
	var containsRoot func(block) bool
	containsRoot = func(b block) bool {
		if b.Mount != nil && *b.Mount == "/" {
			return true
		}
		for _, raw := range b.Children {
			var child block
			if json.Unmarshal(raw, &child) == nil && containsRoot(child) {
				return true
			}
		}
		return false
	}
	walk = func(raw json.RawMessage, bus string) error {
		var b block
		if err := json.Unmarshal(raw, &b); err != nil {
			return err
		}
		if b.Transport != "" {
			bus = b.Transport
		}
		if b.Type == "disk" && containsRoot(b) {
			return nil
		}
		if bus == "usb" && b.Type != "rom" && b.Mount != nil && filepath.IsAbs(*b.Mount) && *b.Mount != "/" {
			if m, ok := mounts[*b.Mount]; ok && sameDevice(m.Device, b.Name) && !seen[m.Root] {
				seen[m.Root] = true
				name := b.Label
				if name == "" {
					name = filepath.Base(m.Root)
				}
				drive := Drive{Device: m.Device, Root: m.Root, Filesystem: b.Filesystem, MountID: m.ID, Writable: m.Writable, Name: name}
				var space syscall.Statfs_t
				if syscall.Statfs(m.Root, &space) == nil {
					drive.Size = space.Blocks * uint64(space.Bsize)
					drive.Free = space.Bavail * uint64(space.Bsize)
				}
				out = append(out, drive)
			}
		}
		for _, child := range b.Children {
			if err := walk(child, bus); err != nil {
				return err
			}
		}
		return nil
	}
	for _, raw := range root.Devices {
		if err := walk(raw, ""); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func sameDevice(a, b string) bool {
	if a == b {
		return true
	}
	left, e := os.Stat(a)
	if e != nil {
		return false
	}
	right, e := os.Stat(b)
	return e == nil && os.SameFile(left, right)
}
func Mounted(d Drive) bool {
	mounts, err := readMountTable()
	if err != nil {
		return false
	}
	m, ok := mounts[d.Root]
	return ok && m.ID == d.MountID && m.Device == d.Device && m.Writable == d.Writable
}
