//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package repl

import (
	"errors"
	"os"
)

func rawMode(*os.File) (func(), error) { return nil, errors.New("no terminal editing on this system") }

func termWidth(*os.File) int { return 0 }

const editorSupported = false
