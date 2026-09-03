package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	speechLookPath          = exec.LookPath
	speechCommandContext    = exec.CommandContext
	waitForSpeechCompletion = waitForSpeechCompletionDefault
)

const speechCompletionGracePeriod = 150 * time.Millisecond

const speechCompletionDirective = "\n[[slnc 1000]]"

// speakAssistantReply sends raw assistant text to the native say executable.
func speakAssistantReply(ctx context.Context, reply string, stdout, stderr io.Writer) (bool, error) {
	sayPath, err := speechLookPath("say")
	if err != nil {
		return false, speechUnavailableError()
	}

	command := speechCommandContext(ctx, sayPath)
	command.Stdin = strings.NewReader(reply + speechCompletionDirective)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return true, err
	}
	if err := waitForSpeechCompletion(ctx, speechCompletionGracePeriod); err != nil {
		return true, err
	}
	return true, nil
}

func waitForSpeechCompletionDefault(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func speechUnavailableError() error {
	return nil
}

func speechTextOutputEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ASH_SAY_TEXT"))) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}
