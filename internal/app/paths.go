package app

import (
	"ash/internal/workspace"
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ashWorkspaceDir returns the canonical workspace directory under the user's home directory.
func ashWorkspaceDir() (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	return workspace.Root(home), nil
}

// ashScratchRoot returns the canonical scratch directory under the user's ash workspace.
func ashScratchRoot() (string, error) {
	workspace, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(workspace, scratchDirName), nil
}

func ashScratchSessionRoot() (string, error) {
	root, err := ashScratchRoot()
	if err != nil {
		return "", err
	}
	sessionID, err := ensureSessionID()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, sessionID), nil
}

func updateScratchAccessMarker(dir string) error {
	if dir == "" {
		return errors.New("scratch directory is required")
	}
	if err := osMkdirAll(dir, 0o700); err != nil {
		return err
	}
	marker := filepath.Join(dir, scratchAccessFileName)
	content := []byte(timeNow().UTC().Format(time.RFC3339Nano))
	return osWriteFile(marker, content, 0o600)
}

func resolveScratchPath(root, userPath string) (absolute string, rel string, err error) {
	return resolveWorkspacePath(root, userPath)
}

// scratchRelativePathIfWithin reports whether candidate (absolute or relative to the
// current working directory) resolves inside root, returning its path relative to root.
// It does not resolve symlinks; it only performs lexical path comparison.
func scratchRelativePathIfWithin(root, candidate string) (rel string, ok bool) {
	if root == "" || candidate == "" {
		return "", false
	}
	absCandidate := candidate
	if !filepath.IsAbs(absCandidate) {
		cwd, err := osGetwd()
		if err != nil {
			return "", false
		}
		absCandidate = filepath.Join(cwd, absCandidate)
	}
	absCandidate = filepath.Clean(absCandidate)
	absRoot := filepath.Clean(root)

	rel, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return "", false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func cleanupStaleScratchDirs(root string, now time.Time) ([]string, error) {
	if root == "" {
		return nil, errors.New("scratch root is required")
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return nil, err
	}

	currentSessionDir, err := ashScratchSessionRoot()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	deleted := make([]string, 0)
	cutoffAge := now.Add(-scratchCleanupMaxAge)
	cutoffIdle := now.Add(-scratchCleanupIdleAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(root, entry.Name())
		if dirPath == currentSessionDir {
			continue
		}
		info, err := os.Stat(dirPath)
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoffAge) {
			continue
		}
		accessPath := filepath.Join(dirPath, scratchAccessFileName)
		accessInfo, err := os.Stat(accessPath)
		if err == nil {
			if accessInfo.ModTime().After(cutoffIdle) {
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.RemoveAll(dirPath); err != nil {
			continue
		}
		deleted = append(deleted, dirPath)
	}
	return deleted, nil
}

// resolveWorkspacePath converts a user-supplied workspace path into a canonical absolute path and a relative workspace path.
func resolveWorkspacePath(root, userPath string) (absolute string, rel string, err error) {
	cleanInput := strings.TrimSpace(userPath)
	if cleanInput == "" {
		return "", "", errors.New("path must be a non-empty string")
	}

	if filepath.IsAbs(cleanInput) {
		relPath, relErr := filepath.Rel(root, cleanInput)
		if relErr != nil {
			return "", "", relErr
		}
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", "", errors.New("path must be inside ~/.ash")
		}
		slashRel := filepath.ToSlash(relPath)
		if hasBlockedDotSegment(slashRel) {
			return "", "", errors.New("path must not reference a hidden dotfile")
		}
		return cleanInput, slashRel, nil
	}

	joined := filepath.Join(root, cleanInput)
	clean := filepath.Clean(joined)
	relPath, relErr := filepath.Rel(root, clean)
	if relErr != nil {
		return "", "", relErr
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path must be inside ~/.ash")
	}
	slashRel := filepath.ToSlash(relPath)
	if hasBlockedDotSegment(slashRel) {
		return "", "", errors.New("path must not reference a hidden dotfile")
	}
	return clean, slashRel, nil
}

// updateWorkspaceInventory updates the workspace inventory file with the supplied file purpose.
func updateWorkspaceInventory(root, relPath, purpose string) error {
	if filepath.ToSlash(relPath) == inventoryFileName {
		return nil
	}

	inventoryPath := filepath.Join(root, inventoryFileName)
	entries := map[string]string{}
	if content, err := osReadFile(inventoryPath); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "|", 2)
			if len(parts) != 2 {
				continue
			}
			entries[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	entries[filepath.ToSlash(relPath)] = strings.TrimSpace(purpose)
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(" | ")
		b.WriteString(entries[key])
		b.WriteString("\n")
	}
	return osWriteFile(inventoryPath, []byte(b.String()), 0o600)
}
