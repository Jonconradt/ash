package main

import (
	"path/filepath"
	"strings"
)

func zshRCPath(home string) string {
	return filepath.Join(home, ".zshrc")
}

func zshWrapperFileName() string {
	return ".ash_zshrc"
}

func zshInstallSourceBlock() string {
	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
[ -f "$HOME/.ash/.ash_zshrc" ] && . "$HOME/.ash/.ash_zshrc"
` + installEndMarker)
}

func zshInstallWrapperContent() string {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_zshrc")
	if err != nil {
		panic("embedded ash_bootstrap/.ash_zshrc is missing: " + err.Error())
	}
	return strings.TrimSpace(string(content))
}
