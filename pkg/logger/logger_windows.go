//go:build windows

package logger

import (
	"os"
	"syscall"
)

func isTerminal(fd uintptr) bool {
	// On Windows, check if the file is a console
	var st uint32
	err := syscall.GetConsoleMode(syscall.Handle(fd), &st)
	return err == nil
}

// IsTerminal checks if the writer is a terminal
func IsTerminal(w *os.File) bool {
	return isTerminal(w.Fd())
}
