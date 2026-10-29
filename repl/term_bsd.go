//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package repl

import "syscall"

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
