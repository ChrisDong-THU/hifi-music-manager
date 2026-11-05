//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	library "hifi.local/library"
)

func listUSBDrives(ctx context.Context) ([]USBDrive, error) { return library.ListDrives(ctx) }
func driveMounted(d USBDrive) bool                          { return library.Mounted(d) }
func runtimeDirectory() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}
func controlPlayer(ctx context.Context, action string) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	switch action {
	case "stop":
		return exec.CommandContext(ctx, "systemctl", "--user", "stop", "hifi-minimserver.service").Run()
	case "ensure":
		if exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", "hifi-minimserver.service").Run() == nil {
			return nil
		}
		return exec.CommandContext(ctx, "systemctl", "--user", "--no-block", "start", "hifi-minimserver.service").Run()
	default:
		return errors.New("unknown library player action")
	}
}
