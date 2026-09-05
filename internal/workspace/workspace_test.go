package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRoot(t *testing.T) {
	home := "/home/testuser"
	expected := filepath.Join(home, DirName)
	if got := Root(home); got != expected {
		t.Errorf("Root(%q) = %q, want %q", home, got, expected)
	}
}

func TestWorkspaceRootAdversarialInputs(t *testing.T) {
	tests := []string{
		"",
		"/",
		"../../../../etc",
		"\x00null",
		strings.Repeat("a", 4096),
		"~",
		"/tmp/test workspace with spaces",
	}

	for _, home := range tests {
		got := Root(home)
		if !strings.HasSuffix(got, DirName) {
			t.Errorf("Root(%q) = %q, expected suffix %q", home, got, DirName)
		}
	}
}
