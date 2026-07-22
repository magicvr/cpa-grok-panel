//go:build windows

package state

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockFileExclusiveLock   = 2
	lockFileFailImmediately = 1
)

func lockFile(f *os.File) error {
	fd := f.Fd()
	overlapped := &syscall.Overlapped{}
	ret, _, err := procLockFileEx.Call(
		fd,
		uintptr(lockFileExclusiveLock|lockFileFailImmediately),
		0,
		1, // lock 1 byte
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if ret == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	fd := f.Fd()
	overlapped := &syscall.Overlapped{}
	ret, _, err := procUnlockFileEx.Call(
		fd,
		0,
		1, // unlock 1 byte
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if ret == 0 {
		return err
	}
	return nil
}
