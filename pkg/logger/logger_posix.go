//go:build !windows

package logger

import (
	"os"
	"syscall"
	"unsafe"
)

const ioctlReadTermios = 0x5401

type termios struct {
	Cflag uint32
	Iflag uint32
	Lflag uint32
	Oflag uint32
	CC    [20]byte
	Ispeed uint32
	Ospeed uint32
}

func isTerminal(fd uintptr) bool {
	var t termios
	_, _, err := syscall.Syscall6(
		syscall.SYS_IOCTL,
		uintptr(fd),
		ioctlReadTermios,
		uintptr(unsafe.Pointer(&t)),
		0, 0, 0,
	)
	return err == 0
}

// IsTerminal checks if the writer is a terminal
func IsTerminal(w *os.File) bool {
	return isTerminal(w.Fd())
}
