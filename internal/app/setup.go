package app

import (
	"errors"
	"os"
	"strings"

	"github.com/Jonconradt/ash/internal/localize"
)

func missingAIEnvSetupError() error {
	rcFile := rcFileForShellEnv(os.Getenv("SHELL"))

	return errors.New(localize.Format("setup.ai_env_incomplete", []any{rcFile, rcFile}))
}

func rcFileForShellEnv(shellEnv string) string {
	normalized := strings.ToLower(strings.TrimSpace(shellEnv))
	if strings.Contains(normalized, "zsh") {
		return ".zshrc"
	}
	if strings.Contains(normalized, "bash") {
		return ".bashrc"
	}
	return ".zshrc"
}
