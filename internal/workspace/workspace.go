// Package workspace resolves the canonical ash workspace directory layout under a
// user's home directory, kept dependency-free for reuse by other internal packages.
package workspace

import "path/filepath"

// DirName is the workspace directory name under the user's home directory.
const DirName = ".ash"

// Root returns the canonical workspace directory path for the given home directory.
func Root(home string) string {
	return filepath.Join(home, DirName)
}
