package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	library "hifi.local/library"
)

// The desktop owns mounting. The service consumes only verified, mounted USB
// filesystems; it never mounts disks, edits fstab or writes below a lost mount.
type USBDrive = library.Drive
type LibraryState = library.State
type libraryChoice struct {
	drive  USBDrive
	rank   int
	volume string
}

func volumeIDFromRoot(root *os.Root) (string, error) {
	project, err := root.OpenRoot(library.DataDirectory)
	if err != nil {
		return "", err
	}
	defer project.Close()
	return library.VolumeID(project)
}

func rankDrive(d USBDrive) (libraryChoice, error) {
	choice := libraryChoice{drive: d}
	if !d.Writable {
		return choice, errors.New("音乐盘只读")
	}
	info := library.InspectDisk(d)
	if info.Issue != "" {
		return choice, errors.New(info.Issue)
	}
	if info.Initialized {
		choice.rank = 3
		choice.volume = info.VolumeID
	}
	return choice, nil
}
func sameDrive(a, b USBDrive) bool {
	return a.Device == b.Device && a.Root == b.Root && a.MountID == b.MountID
}
func chooseDrives(drives []USBDrive, current *libraryChoice) ([]libraryChoice, []string) {
	choices := []libraryChoice{}
	issues := []string{}
	for _, drive := range drives {
		choice, err := rankDrive(drive)
		if err != nil {
			issues = append(issues, filepath.Base(drive.Root)+"："+err.Error())
			continue
		}
		choices = append(choices, choice)
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].rank != choices[j].rank {
			return choices[i].rank > choices[j].rank
		}
		if current != nil {
			a, b := sameDrive(choices[i].drive, current.drive), sameDrive(choices[j].drive, current.drive)
			if a != b {
				return a
			}
		}
		if choices[i].drive.Root != choices[j].drive.Root {
			return choices[i].drive.Root < choices[j].drive.Root
		}
		return choices[i].drive.Device < choices[j].drive.Device
	})
	return choices, issues
}

func (a *LibraryService) pausePath() string {
	return filepath.Join(runtimeDirectory(), "hifi-library.paused")
}
func (a *LibraryService) writeLibraryState(state LibraryState) error {
	return library.WriteState(a.home, state)
}
func (a *LibraryService) linkLibrary(music string) error {
	link := filepath.Join(a.home, "music")
	if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("music 已存在且不是符号链接，保持原目录")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	pending := filepath.Join(a.home, "music.next-"+newID())
	if err := os.Symlink(music, pending); err != nil {
		return err
	}
	defer os.Remove(pending)
	return os.Rename(pending, link)
}

type libraryWatcher struct {
	a           *LibraryService
	current     *libraryChoice
	list        func(context.Context) ([]USBDrive, error)
	check       func(USBDrive) bool
	player      func(context.Context, string) error
	link        func(string) error
	online      func(string) (string, error)
	cancelIndex func()
	lastState   string
	scanID      string
	scanRetry   time.Time
}

func (w *libraryWatcher) publish(state LibraryState) error {
	state.UpdatedAt = ""
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	key := string(data)
	if key == w.lastState {
		return nil
	}
	if err := w.a.writeLibraryState(state); err != nil {
		return err
	}
	w.lastState = key
	return nil
}
func (w *libraryWatcher) offline(ctx context.Context, state LibraryState) error {
	if w.cancelIndex != nil {
		w.cancelIndex()
	}
	err := w.publish(state)
	if w.current != nil {
		if stopErr := w.player(ctx, "stop"); stopErr != nil {
			return errors.Join(err, stopErr)
		}
		w.current = nil
	}
	return err
}
func (w *libraryWatcher) step(ctx context.Context) error {
	state := LibraryState{Available: []string{}}
	if _, e := os.Stat(w.a.pausePath()); e == nil {
		state.Paused = true
		state.Message = "曲库已暂停"
		return w.offline(ctx, state)
	}
	drives, err := w.list(ctx)
	if err != nil {
		state.Message = "暂时无法读取 USB 挂载状态"
		return errors.Join(err, w.offline(ctx, state))
	}
	// Stop the old player before selecting another disk or mount generation.
	if w.current != nil && !w.check(w.current.drive) {
		if err = w.offline(ctx, state); err != nil {
			return err
		}
	}
	choices, issues := chooseDrives(drives, w.current)
	state.Issues = issues
	preference, prefErr := library.ReadPreference(w.a.home)
	if prefErr != nil {
		state.Message = prefErr.Error()
		return w.offline(ctx, state)
	}
	for _, choice := range choices {
		state.Available = append(state.Available, library.MusicPath(choice.drive.Root))
	}
	for _, choice := range choices {
		if choice.rank < 3 || (preference.VolumeID != "" && preference.VolumeID != choice.volume) {
			continue
		}
		if w.current != nil && sameDrive(choice.drive, w.current.drive) && choice.volume == w.current.volume {
			state.Selected = library.MusicPath(choice.drive.Root)
			state.Mount = choice.drive.Root
			state.DataRoot = library.DataPath(choice.drive.Root)
			state.Volume = choice.volume
			state.Device = choice.drive.Device
			state.MountID = choice.drive.MountID
			if err = w.publish(state); err != nil {
				return err
			}
			if err = w.player(ctx, "ensure"); err != nil {
				return err
			}
			return w.ensureIndex()
		}
		if w.current != nil {
			if err = w.offline(ctx, state); err != nil {
				return err
			}
		}
		root, e := os.OpenRoot(choice.drive.Root)
		if e != nil {
			continue
		}
		id, e := volumeIDFromRoot(root)
		root.Close()
		if e != nil || id != choice.volume {
			if e == nil {
				e = errors.New("曲库标识已改变，请重新选择数据盘")
			}
			state.Issues = append(state.Issues, filepath.Base(choice.drive.Root)+"："+e.Error())
			continue
		}
		if !w.check(choice.drive) {
			continue
		}
		choice.volume = id
		music := library.MusicPath(choice.drive.Root)
		if e = w.link(music); e != nil {
			state.Issues = append(state.Issues, e.Error())
			continue
		}
		if !w.check(choice.drive) {
			continue
		}
		state.Selected = music
		state.Mount = choice.drive.Root
		state.DataRoot = library.DataPath(choice.drive.Root)
		state.Volume = id
		state.Device = choice.drive.Device
		state.MountID = choice.drive.MountID
		if e = w.publish(state); e != nil {
			return e
		}
		w.current = &choice
		w.scanID = ""
		w.scanRetry = time.Time{}
		log.Printf("USB music library online: %s", music)
		if e = w.player(ctx, "ensure"); e != nil {
			return e
		}
		return w.ensureIndex()
	}
	state.Message = "请选择或初始化数据盘"
	return w.offline(ctx, state)
}
func (w *libraryWatcher) ensureIndex() error {
	if w.current == nil || w.scanID != "" || time.Now().Before(w.scanRetry) {
		return nil
	}
	id, err := w.online(w.current.volume)
	if err != nil {
		w.scanRetry = time.Now().Add(2 * time.Second)
		return err
	}
	w.scanID = id
	return nil
}
func (a *LibraryService) watchLibrary(ctx context.Context) {
	refresh := indexRefresh{request: requestRescan}
	defer refresh.stop()
	w := libraryWatcher{a: a, list: listUSBDrives, check: driveMounted, player: controlPlayer, link: a.linkLibrary, online: func(volume string) (string, error) {
		refresh.start(ctx)
		return volume, nil
	}, cancelIndex: refresh.stop}
	// Ignore the obsolete persistent pause marker. Pauses now live in /run/user
	// and are deliberately cleared by the OS when the machine reboots.
	_ = a.writeLibraryState(LibraryState{Available: []string{}, Message: "等待桌面挂载 USB 音乐盘"})
	_ = controlPlayer(ctx, "stop")
	defer func() {
		_ = a.writeLibraryState(LibraryState{Available: []string{}, Message: "音乐服务已停止"})
		stop, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = controlPlayer(stop, "stop")
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastError := ""
	for {
		if err := w.step(ctx); err != nil && ctx.Err() == nil {
			if err.Error() != lastError {
				log.Printf("USB music library: %v", err)
				lastError = err.Error()
			}
		} else {
			lastError = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
