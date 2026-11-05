//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func listUSBDrives(context.Context) ([]USBDrive, error) {
	return nil, errors.New("USB auto-discovery runs on the Linux music server")
}
func driveMounted(USBDrive) bool { return false }
func runtimeDirectory() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "hifi-runtime")
}
func controlPlayer(context.Context, string) error { return nil }
