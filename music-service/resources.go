package main

import (
	"os"
	"path/filepath"

	library "hifi.local/library"
)

func (a *LibraryService) libraryKey() string {
	state, err := library.ReadState(a.home)
	if err != nil || state.ValidateSelection() != nil {
		return "offline"
	}
	return state.Volume + ":" + state.MountID
}

func (a *LibraryService) openArtists() (*os.Root, error) {
	state, err := library.ReadState(a.home)
	if err != nil {
		return nil, err
	}
	if state.ValidateSelection() != nil {
		return nil, errOffline
	}
	drive := USBDrive{Root: state.Mount, Device: state.Device, MountID: state.MountID, Writable: true}
	if !driveMounted(drive) {
		return nil, errOffline
	}
	root, err := os.OpenRoot(drive.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	mountID, err := library.DirectoryMountID(root)
	if err != nil || mountID != state.MountID {
		return nil, errOffline
	}
	id, err := volumeIDFromRoot(root)
	if err != nil || id != state.Volume {
		return nil, errOffline
	}
	return root.OpenRoot(filepath.Join(library.DataDirectory, filepath.FromSlash(library.ArtistDirectory)))
}
