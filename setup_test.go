package main

import (
	"strings"
	"testing"
)

func TestRCFileForShellEnv(t *testing.T) {
	tests := []struct {
		name     string
		shellEnv string
		want     string
	}{
		{name: "zsh path", shellEnv: "/bin/zsh", want: ".zshrc"},
		{name: "bash path", shellEnv: "/opt/homebrew/bin/bash", want: ".bashrc"},
		{name: "zsh with flags", shellEnv: "/bin/zsh -l", want: ".zshrc"},
		{name: "unknown defaults to zshrc", shellEnv: "/bin/fish", want: ".zshrc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rcFileForShellEnv(tt.shellEnv); got != tt.want {
				t.Fatalf("rcFileForShellEnv(%q)=%q want %q", tt.shellEnv, got, tt.want)
			}
		})
	}
}

func TestMissingAIEnvSetupError(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	err := missingAIEnvSetupError()
	if err == nil {
		t.Fatal("expected setup error, got nil")
	}
	message := err.Error()
	for _, want := range []string{
		"~/.zshrc",
		"export AI_ENDPOINT='https://your-api-endpoint.example.com'",
		"export AI_MODEL='your-model-name'",
		"export AI_AUTH_TOKEN='your-token'",
		"source ~/.zshrc",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected zsh guidance to contain %q, got %q", want, message)
		}
	}

	t.Setenv("SHELL", "/bin/bash")
	err = missingAIEnvSetupError()
	if err == nil {
		t.Fatal("expected setup error, got nil")
	}
	if !strings.Contains(err.Error(), "source ~/.bashrc") {
		t.Fatalf("expected bash source guidance, got %q", err.Error())
	}
}
