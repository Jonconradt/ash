package app

import (
	"os"
	"path/filepath"
	"strings"
)

func fishRCPath(home string) string {
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" || !filepath.IsAbs(configHome) {
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
	if not set -q __ash_wrap_command_not_found
		if functions -q fish_command_not_found
			functions -c fish_command_not_found __ash_original_command_not_found
		end
		set -g __ash_wrap_command_not_found 1
	end
	function fish_command_not_found
		if command -sq ash
			command ash -- $argv
			return $status
		end
		if functions -q __ash_original_command_not_found
			__ash_original_command_not_found $argv
			return $status
		end
		return 127
	end
end
` + installEndMarker)
}
