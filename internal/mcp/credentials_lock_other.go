//go:build !darwin && !freebsd && !linux

package mcp

import (
	"os"
	"sync"
)

var credentialFileLocks sync.Map

func lockCredentialFile(file *os.File) (func() error, error) {
	lock, _ := credentialFileLocks.LoadOrStore(file.Name(), &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return func() error {
		mutex.Unlock()
		return nil
	}, nil
}
