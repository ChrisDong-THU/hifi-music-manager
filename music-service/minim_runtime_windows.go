//go:build windows

package main

import "errors"

func runMinimRuntime(string, serviceConfig) error {
	return errors.New("MinimServer USB runtime requires Linux")
}
