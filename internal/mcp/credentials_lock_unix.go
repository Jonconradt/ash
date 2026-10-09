//go:build darwin || freebsd || linux

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockCredentialFile(file *os.File) (func() error, error) {
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			if err != nil {
				return nil, err
			}
			break
		}
	}
	return func() error {
		return unix.Flock(int(file.Fd()), unix.LOCK_UN)
	}, nil
}
