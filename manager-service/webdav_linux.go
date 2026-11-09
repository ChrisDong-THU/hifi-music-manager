package main

import "syscall"

// A replacement FIFO must not block Open before we can verify its type.
const davReadFlags = syscall.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
