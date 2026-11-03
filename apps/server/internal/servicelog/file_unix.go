//go:build darwin || linux

package servicelog

import (
	"errors"
	"os"
	"syscall"
)

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
func ownedSingle(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && owned(info) && stat.Nlink == 1
}
func openPrivate(name string, flags int) (*os.File, error) {
	file, err := os.OpenFile(name, flags|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedSingle(info) {
		_ = file.Close()
		return nil, errors.New("Refusing an unowned, linked or shared service log")
	}
	return file, nil
}

func lockPrivate(name string) (*os.File, error) {
	file, err := openPrivate(name, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("Another Camp service owns this log stream")
	}
	return file, nil
}
