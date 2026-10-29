//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package repl

import (
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// rawMode makes the terminal on f hand over each key as it is pressed: no
// line buffering, no echo, and Ctrl-C as a byte and not a signal. Output
// processing stays on, so \n still ends a line. The function it returns puts
// the terminal back.
func rawMode(f *os.File) (restore func(), err error) {
	var old syscall.Termios
	if err := ioctl(f.Fd(), ioctlGetTermios, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.ICRNL | syscall.INLCR | syscall.IXON | syscall.ISTRIP
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG | syscall.IEXTEN
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(f.Fd(), ioctlSetTermios, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return func() { _ = ioctl(f.Fd(), ioctlSetTermios, unsafe.Pointer(&old)) }, nil
}

// termWidth is the number of columns of the terminal on f, or 0 if unknown.
func termWidth(f *os.File) int {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(f.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0
	}
	return int(ws.Col)
}

const editorSupported = true
