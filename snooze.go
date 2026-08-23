package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSnoozeDuration = 5 * time.Minute
	snoozeFileName        = ".ash_snooze_until"
)

func runSnooze(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		_, _ = fmt.Fprintln(stderr, "usage: ash snooze [duration|off]")
		return 1
	}

	if len(args) == 1 && strings.EqualFold(strings.TrimSpace(args[0]), "off") {
		if err := clearSnooze(); err != nil {
			_, _ = fmt.Fprintf(stderr, "failed to clear snooze: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "ash prompt processing resumed")
		return 0
	}

	duration := defaultSnoozeDuration
	if len(args) == 1 {
		parsed, err := time.ParseDuration(strings.TrimSpace(args[0]))
		if err != nil || parsed <= 0 {
			_, _ = fmt.Fprintln(stderr, "duration must be a positive value such as 30s, 5m, or 1h")
			return 1
		}
		duration = parsed
	}

	expiresAt := timeNow().Add(duration)
	if err := writeSnoozeExpiry(expiresAt); err != nil {
		_, _ = fmt.Fprintf(stderr, "failed to start snooze: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "ash prompt processing snoozed until %s\n", expiresAt.Format(time.RFC3339))
	return 0
}

func snoozeFilePath() (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, snoozeFileName), nil
}

func writeSnoozeExpiry(expiresAt time.Time) error {
	path, err := snoozeFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), snoozeFileName+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.WriteString(temporary, strconv.FormatInt(expiresAt.Unix(), 10)+"\n"); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func clearSnooze() error {
	path, err := snoozeFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func snoozeActive() bool {
	path, err := snoozeFilePath()
	if err != nil {
		return false
	}
	content, err := osReadFile(path)
	if err != nil {
		return false
	}
	expiresAt, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	return err == nil && expiresAt > timeNow().Unix()
}
