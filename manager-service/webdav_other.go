//go:build !linux

package main

import "os"

const davReadFlags = os.O_RDONLY
