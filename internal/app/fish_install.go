package app

import (
	"os"
	"path/filepath"
	"strings"
)

func fishRCPath(home string) string {
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "fish", "config.fish")
}

func fishWrapperFileName() string {
	return ".ash_fish.fish"
}

func fishInstallSourceBlock() string {
	return installSourceBlockFromAsset("ash_bootstrap/rc-source-fish.fish")
}

func fishInstallWrapperContent() string {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_fish.fish")
	if err == nil {
		return strings.TrimSpace(string(content))
	}
	return fallbackFishInstallWrapperContent()
}

func fallbackFishInstallWrapperContent() string {
	return strings.TrimSpace(`
` + installStartMarker + `
if status is-interactive
	function fish_command_not_found
		command ash $argv
	end
end
` + installEndMarker)
}
