// Package workspace resolves the canonical ash workspace directory layout under a
// user's home directory, kept dependency-free for reuse by other internal packages.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirName is the ash subdirectory used beneath XDG base directories.
const DirName = "ash"

func baseDir(envName, fallback string) string {
	if path := strings.TrimSpace(os.Getenv(envName)); filepath.IsAbs(path) {
		return filepath.Join(path, DirName)
	}
	return filepath.Join(fallback, DirName)
}

// Root returns ash's configuration directory.
func Root(home string) string {
	return baseDir("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

// DataRoot returns ash's durable data directory.
func DataRoot(home string) string {
	return baseDir("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
}

// StateRoot returns ash's persistent state directory.
func StateRoot(home string) string {
	return baseDir("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
}

// CacheRoot returns ash's regenerable cache directory.
func CacheRoot(home string) string {
	return baseDir("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
}

// RuntimeRoot returns ash's runtime directory, falling back to state storage when
// XDG_RUNTIME_DIR is unavailable or invalid.
func RuntimeRoot(home string) string {
	if path := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); filepath.IsAbs(path) {
		return filepath.Join(path, DirName)
	}
	return filepath.Join(StateRoot(home), "runtime")
}

// ValidateBaseDirs rejects explicitly configured XDG paths that are not absolute.
func ValidateBaseDirs() error {
	for _, name := range []string{
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_STATE_HOME",
		"XDG_CACHE_HOME",
		"XDG_RUNTIME_DIR",
	} {
		if path := strings.TrimSpace(os.Getenv(name)); path != "" && !filepath.IsAbs(path) {
			return fmt.Errorf("%s must be an absolute path", name)
		}
	}
	return nil
}
