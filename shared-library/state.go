package library

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const maxStateBytes = 1 << 20

// State is the supervisor's public snapshot. All consumers use the same
// selection contract; the on-disk field names and identifiers stay stable.
type State struct {
	Selected  string   `json:"selected"`
	Mount     string   `json:"mount"`
	DataRoot  string   `json:"data_root"`
	Volume    string   `json:"volume,omitempty"`
	Device    string   `json:"device,omitempty"`
	MountID   string   `json:"mount_id,omitempty"`
	Available []string `json:"available"`
	Paused    bool     `json:"paused"`
	Message   string   `json:"message,omitempty"`
	Issues    []string `json:"issues,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

// ValidateSelection checks the snapshot, not the live mount. Callers must still
// verify the mounted filesystem and its identity before accessing disk data.
func (s State) ValidateSelection() error {
	if s.Paused || !ValidID(s.Volume) || s.MountID == "" || s.Selected == "" {
		return errors.New("等待音乐盘就绪")
	}
	if !filepath.IsAbs(s.Mount) || filepath.Clean(s.Mount) != s.Mount ||
		s.Selected != MusicPath(s.Mount) || s.DataRoot != DataPath(s.Mount) {
		return errors.New("数据盘目录结构无效")
	}
	return nil
}

func ReadState(home string) (State, error) {
	var state State
	root, err := os.OpenRoot(filepath.Join(home, "logs"))
	if err != nil {
		return state, err
	}
	defer root.Close()
	data, err := read(root, "library-state.json", maxStateBytes)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	return state, err
}

// WriteState publishes a complete snapshot with a unique temporary name. A
// failed write cannot truncate the last snapshot or follow a stale .next link.
func WriteState(home string, state State) error {
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return errors.New("library state exceeds 1 MiB")
	}
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return atomicMode(root, "library-state.json", data, 0600)
}
