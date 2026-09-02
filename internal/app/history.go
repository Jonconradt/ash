package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sortedAllowlist returns the allowlisted tool names in sorted order.
func sortedAllowlist(allowlist map[string]struct{}) []string {
	out := make([]string, 0, len(allowlist))
	for name := range allowlist {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// stripSystemMessage removes the leading system message when present.
func stripSystemMessage(messages []message) []message {
	if len(messages) == 0 {
		return nil
	}
	if messages[0].Role == "system" {
		return append([]message(nil), messages[1:]...)
	}
	return append([]message(nil), messages...)
}

// getHistoryPath returns the path to the per-session history file used for chat state.
func getHistoryPath() (string, error) {
	if _, err := ensureSessionID(); err != nil {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	historyDir := filepath.Join(home, ashWorkspaceDirName, historyDirName)
	if err := osMkdirAll(historyDir, 0o700); err != nil {
		return "", err
	}

	sessionID, err := sanitizedSessionIDForLogFile()
	if err != nil {
		return "", err
	}

	filename := sessionID + ".json"
	if isScheduledTaskRun() {
		filename = "task_" + sessionID + ".json"
	}

	return filepath.Join(historyDir, filename), nil
}

// isScheduledTaskRun reports whether the current process is running as a scheduled ash task.
func isScheduledTaskRun() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(scheduledTaskEnvName)))
	return raw == "1" || raw == "true" || raw == "yes"
}

// cleanupWorkspaceRetention sweeps aged history and broker runtime files, sharing one
// time budget so a slow filesystem cannot stall exit twice over.
func cleanupWorkspaceRetention(maxAge, budget time.Duration) {
	if maxAge <= 0 || budget <= 0 {
		return
	}
	deadline := timeNow().Add(budget)
	cleanupHistoryRetention(maxAge, deadline)
	cleanupRuntimeRetention(maxAge, deadline)
}

// cleanupHistoryRetention removes history files older than maxAge until the deadline passes.
func cleanupHistoryRetention(maxAge time.Duration, deadline time.Time) {
	home, err := osUserHomeDir()
	if err != nil {
		return
	}
	historyDir := filepath.Join(home, ashWorkspaceDirName, historyDirName)
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return
	}

	cutoff := timeNow().Add(-maxAge)
	for _, entry := range entries {
		if timeNow().After(deadline) {
			return
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(historyDir, entry.Name()))
	}
}

// cleanupRuntimeRetention removes broker lease, pid, and socket files left behind by shells
// that exited without running their shutdown trap. A socket with a live listener is kept
// regardless of age; the broker ignores the lease file, so removing one is always safe.
func cleanupRuntimeRetention(maxAge time.Duration, deadline time.Time) {
	home, err := osUserHomeDir()
	if err != nil {
		return
	}
	runtimeDir := filepath.Join(home, ashWorkspaceDirName, runtimeDirName)
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return
	}

	cutoff := timeNow().Add(-maxAge)
	for _, entry := range entries {
		if timeNow().After(deadline) {
			return
		}
		if entry.IsDir() {
			continue
		}
		extension := filepath.Ext(entry.Name())
		if extension != ".lease" && extension != ".pid" && extension != ".sock" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(runtimeDir, entry.Name())
		if extension == ".sock" && brokerSocketAlive(path) {
			continue
		}
		_ = os.Remove(path)
	}
}

// brokerSocketAlive reports whether a broker is still listening on the given socket path.
func brokerSocketAlive(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), brokerSocketProbeTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

// loadHistory reads chat history from the provided JSON file path.
func loadHistory(path string) (historyData, error) {
	content, err := osReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return historyData{Conversations: map[string][]message{}}, nil
	}
	if err != nil {
		return historyData{}, err
	}

	var data historyData
	if err := json.Unmarshal(content, &data); err != nil {
		return historyData{}, err
	}
	if data.Conversations == nil {
		data.Conversations = map[string][]message{}
	}

	return data, nil
}

// saveHistory writes chat history to the provided JSON file path.
func saveHistory(path string, data historyData) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return osWriteFile(path, content, 0o600)
}
