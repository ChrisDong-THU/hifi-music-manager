//go:build !windows

package main

import "os"

func restrictAuthFile(filename string) error {
	return os.Chmod(filename, 0600)
}
