package main

import (
	"errors"
	"os"

	library "hifi.local/library"
)

type librarySelection struct {
	drive library.Drive
	id    string
}

func storageForSelection(state librarySelection) (string, string, func() error, error) {
	volume := state.drive.Root
	guard, err := volumeGuard(volume)
	if err != nil {
		return "", "", nil, err
	}
	if err := selectionMount(volume, state.drive.MountID); err != nil {
		return "", "", nil, err
	}
	root, err := os.OpenRoot(library.DataPath(volume))
	if err != nil {
		return "", "", nil, err
	}
	defer root.Close()
	id, err := library.VolumeID(root)
	if err != nil || id != state.id {
		return "", "", nil, errors.New("音乐盘标识已改变")
	}
	return volume, library.ManagerPath(volume), guard, nil
}
