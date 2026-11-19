package main

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsTerminal reports whether stdin is a terminal (a TIOCGETA ioctl succeeds).
func stdinIsTerminal() bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
