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
	return installSourceBlockFromAsset("ash_bootstrap/rc-source-zsh.sh")
}

func zshInstallWrapperContent() string {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_zshrc")
	if err != nil {
		panic("embedded ash_bootstrap/.ash_zshrc is missing: " + err.Error())
	}
	return strings.TrimSpace(string(content))
}
