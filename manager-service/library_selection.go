package main

import (
	"errors"
	"os"

	library "hifi.local/library"
)

type librarySelection = library.State

func storageForSelection(state librarySelection) (string, string, func() error, error) {
	if err := state.ValidateSelection(); err != nil {
		return "", "", nil, err
	}
	volume := state.Mount
	guard, err := volumeGuard(volume)
	if err != nil {
		return "", "", nil, err
	}
	if err := selectionMount(volume, state.MountID); err != nil {
		return "", "", nil, err
	}
	root, err := os.OpenRoot(library.DataPath(volume))
	if err != nil {
		return "", "", nil, err
	}
	defer root.Close()
	id, err := library.VolumeID(root)
	if err != nil || id != state.Volume {
		return "", "", nil, errors.New("音乐盘标识与监督服务不一致")
	}
	return volume, library.ManagerPath(volume), guard, nil
}

func readLibrarySelection(project string) (librarySelection, error) {
	return library.ReadState(project)
}
