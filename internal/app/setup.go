package app

import (
	"fmt"
	"os"
	"strings"
)

func missingAIEnvSetupError() error {
	rcFile := rcFileForShellEnv(os.Getenv("SHELL"))

	return fmt.Errorf(
		"AI environment setup is incomplete.\n"+
			"Please add these lines to your ~/%s file:\n\n"+
			"export AI_ENDPOINT='https://your-api-endpoint.example.com'\n"+
			"export AI_MODEL='your-model-name'\n"+
			"export AI_AUTH_TOKEN='your-token'\n\n"+
			"Then run: source ~/%s\n"+
			"After that, run your ash command again",
		rcFile,
		rcFile,
	)
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
