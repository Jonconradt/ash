//go:build darwin || freebsd || linux

package scratchlife

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	lockFileName  = ".ash_scratch_lock"
	guardFileName = ".ash_scratch_guard"
	maxSessionID  = 128
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)?$`)

type Lock struct {
	root      string
	sessionID string
	file      *os.File
	closed    bool
}

func ValidateSessionID(sessionID string) error {
	if len(sessionID) > maxSessionID || !sessionIDPattern.MatchString(sessionID) {
		return errors.New("invalid scratch session ID")
	}
	return nil
}

func SessionDir(root, sessionID string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("scratch root is required")
	}
	if err := ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	return filepath.Join(root, sessionID), nil
}

func Acquire(root, sessionID string) (*Lock, error) {
	sessionDir, err := SessionDir(root, sessionID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create scratch root: %w", err)
	}
	guard, err := acquireGuard(root)
	if err != nil {
		return nil, fmt.Errorf("lock scratch lifecycle: %w", err)
	}
	defer releaseGuard(guard)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("create scratch session: %w", err)
	}
	info, err := os.Lstat(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("inspect scratch session: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("scratch session path is not a directory")
	}
	file, err := openLockFile(filepath.Join(sessionDir, lockFileName))
	if err != nil {
		return nil, fmt.Errorf("open scratch session lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("set scratch session lock permissions: %w", err)
	}
	if err := flock(int(file.Fd()), unix.LOCK_SH); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("hold scratch session lock: %w", err)
	}
	return &Lock{root: root, sessionID: sessionID, file: file}, nil
}

func (lock *Lock) Close() (retErr error) {
	if lock == nil || lock.closed {
		return nil
	}
	guard, err := acquireGuard(lock.root)
	if err != nil {
		return fmt.Errorf("lock scratch lifecycle for release: %w", err)
	}
	defer releaseGuard(guard)
	lock.closed = true
	if err := lock.file.Close(); err != nil {
		return fmt.Errorf("release scratch session lock: %w", err)
	}
	sessionDir, err := SessionDir(lock.root, lock.sessionID)
	if err != nil {
		return err
	}
	path := filepath.Join(sessionDir, lockFileName)
	file, err := openExistingLockFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reopen scratch session lock: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close scratch session lock: %w", err)
		}
	}()
	if err := flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil
		}
		return fmt.Errorf("check scratch session lock holders: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove scratch session lock: %w", err)
	}
	return nil
}

type CleanupOptions struct {
	MaxAge  time.Duration
	IdleAge time.Duration
	Now     time.Time
}

func Cleanup(root string, options CleanupOptions) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("scratch root is required")
	}
	if options.MaxAge <= 0 || options.IdleAge <= 0 {
		return nil, errors.New("scratch cleanup ages must be positive")
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create scratch root: %w", err)
	}
	guard, err := acquireGuard(root)
	if err != nil {
		return nil, fmt.Errorf("lock scratch lifecycle for cleanup: %w", err)
	}
	defer releaseGuard(guard)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read scratch root: %w", err)
	}
	cutoffAge := options.Now.Add(-options.MaxAge)
	cutoffIdle := options.Now.Add(-options.IdleAge)
	var deleted []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		sessionID := entry.Name()
		sessionDir, err := SessionDir(root, sessionID)
		if err != nil {
			continue
		}
		info, err := os.Stat(sessionDir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return deleted, fmt.Errorf("inspect scratch session %q: %w", sessionID, err)
			}
			continue
		}
		if info.ModTime().After(cutoffAge) {
			continue
		}
		accessInfo, err := os.Stat(filepath.Join(sessionDir, ".ash_scratch_access"))
		if err == nil && accessInfo.ModTime().After(cutoffIdle) {
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		lockPath := filepath.Join(sessionDir, lockFileName)
		file, err := openExistingLockFile(lockPath)
		if err == nil {
			err = flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
			if err != nil {
				_ = file.Close()
				if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
					continue
				}
				return deleted, fmt.Errorf("check scratch session %q lock: %w", sessionID, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return deleted, fmt.Errorf("inspect scratch session %q lock: %w", sessionID, err)
		}
		if err := os.RemoveAll(sessionDir); err != nil {
			if file != nil {
				_ = file.Close()
			}
			return deleted, fmt.Errorf("remove stale scratch session %q: %w", sessionID, err)
		}
		if file != nil {
			_ = file.Close()
		}
		deleted = append(deleted, sessionDir)
	}
	return deleted, nil
}

func acquireGuard(root string) (*os.File, error) {
	file, err := openLockFile(filepath.Join(root, guardFileName))
	if err != nil {
		return nil, err
	}
	if err := flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseGuard(file *os.File) {
	if file == nil {
		return
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	_ = file.Close()
}

func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create scratch lifecycle file")
	}
	if err := validateLifecycleFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func openExistingLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open scratch lifecycle file")
	}
	if err := validateLifecycleFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func validateLifecycleFile(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("scratch lifecycle path is not a regular file")
	}
	return nil
}

func flock(fd int, operation int) error {
	for {
		err := unix.Flock(fd, operation)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
