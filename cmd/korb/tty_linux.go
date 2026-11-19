package main

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsTerminal reports whether stdin is a terminal (a TCGETS ioctl succeeds).
// Checking for a character device is not enough: /dev/null is one, too.
func stdinIsTerminal() bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
