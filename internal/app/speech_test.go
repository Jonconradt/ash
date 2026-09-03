package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpeakAssistantReplyPassesRawTextToNativeSay(t *testing.T) {
	testDir := t.TempDir()
	capturePath := filepath.Join(testDir, "spoken.txt")
	sayPath := filepath.Join(testDir, "say")
	shouldNotRunPath := filepath.Join(testDir, "should-not-run")
	script := "#!/bin/sh\ncat > \"$SPEECH_CAPTURE\"\nprintf 'native stdout'\nprintf 'native stderr' >&2\n"
	if err := os.WriteFile(sayPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake say: %v", err)
	}

	originalLookPath := speechLookPath
	t.Cleanup(func() { speechLookPath = originalLookPath })
	speechLookPath = func(string) (string, error) {
		return sayPath, nil
	}
	originalWait := waitForSpeechCompletion
	t.Cleanup(func() { waitForSpeechCompletion = originalWait })
	var waited time.Duration
	waitForSpeechCompletion = func(_ context.Context, duration time.Duration) error {
		waited = duration
		return nil
	}
	t.Setenv("SPEECH_CAPTURE", capturePath)

	var stdout, stderr bytes.Buffer
	reply := "quoted 'text'\n$(touch should-not-run)\n"
	spoken, err := speakAssistantReply(context.Background(), reply, &stdout, &stderr)
	if err != nil {
		t.Fatalf("speakAssistantReply returned error: %v", err)
	}
	if !spoken {
		t.Fatal("expected native say to be detected")
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured speech: %v", err)
	}
	wantSpeechInput := reply + speechCompletionDirective
	if string(captured) != wantSpeechInput {
		t.Fatalf("native say received %q, want %q", string(captured), wantSpeechInput)
	}
	if stdout.String() != "native stdout" {
		t.Fatalf("unexpected native stdout: %q", stdout.String())
	}
	if stderr.String() != "native stderr" {
		t.Fatalf("unexpected native stderr: %q", stderr.String())
	}
	if waited != speechCompletionGracePeriod {
		t.Fatalf("speech completion wait was %s, want %s", waited, speechCompletionGracePeriod)
	}
	if _, err := os.Stat(shouldNotRunPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("speech text was unexpectedly executed")
	}
}

func TestSpeakAssistantReplyMissingNativeSay(t *testing.T) {
	originalLookPath := speechLookPath
	t.Cleanup(func() { speechLookPath = originalLookPath })
	speechLookPath = func(string) (string, error) {
		return "", errors.New("not found")
	}

	spoken, err := speakAssistantReply(context.Background(), "hello", &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("missing say returned error: %v", err)
	}
	if spoken {
		t.Fatal("expected missing say to report spoken=false")
	}
}

func TestSpeechTextOutputEnabled(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "on", "enabled"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ASH_SAY_TEXT", value)
			if !speechTextOutputEnabled() {
				t.Fatalf("expected %q to enable text output", value)
			}
		})
	}
	for _, value := range []string{"", "0", "false", "off", "disabled", "unexpected"} {
		t.Run("disabled_"+value, func(t *testing.T) {
			t.Setenv("ASH_SAY_TEXT", value)
			if speechTextOutputEnabled() {
				t.Fatalf("expected %q to leave speech enabled", value)
			}
		})
	}
}

func TestSpeakAssistantReplyReturnsNativeSayFailure(t *testing.T) {
	sayPath := filepath.Join(t.TempDir(), "say")
	if err := os.WriteFile(sayPath, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatalf("write fake say: %v", err)
	}
	originalLookPath := speechLookPath
	t.Cleanup(func() { speechLookPath = originalLookPath })
	speechLookPath = func(string) (string, error) {
		return sayPath, nil
	}

	spoken, err := speakAssistantReply(context.Background(), "hello", &bytes.Buffer{}, &bytes.Buffer{})
	if !spoken {
		t.Fatal("expected native say to be detected")
	}
	if err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("expected native say exit error, got %v", err)
	}
}

func TestRunSayModeSpeaksAssistantReply(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SESSION_ID", "say-test")
	t.Setenv("AI_ENDPOINT", "http://localhost:11434")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("ASH_VERBOSE", "0")
	t.Setenv("ASH_STREAM", "0")
	t.Setenv(ashEnvAlwaysOpenAIAPI, "0")

	originalChatStreamExecutor := chatStreamExecutor
	t.Cleanup(func() { chatStreamExecutor = originalChatStreamExecutor })
	chatStreamExecutor = func(context.Context, aiConfig, []message, []toolDefinition, func(streamDelta)) (chatResponse, error) {
		return chatResponse{Message: message{Role: "assistant", Content: "spoken response"}}, nil
	}

	testDir := t.TempDir()
	capturePath := filepath.Join(testDir, "spoken.txt")
	sayPath := filepath.Join(testDir, "say")
	if err := os.WriteFile(sayPath, []byte("#!/bin/sh\ncat > \"$SPEECH_CAPTURE\"\n"), 0o700); err != nil {
		t.Fatalf("write fake say: %v", err)
	}
	originalLookPath := speechLookPath
	t.Cleanup(func() { speechLookPath = originalLookPath })
	speechLookPath = func(string) (string, error) { return sayPath, nil }
	t.Setenv("SPEECH_CAPTURE", capturePath)

	var stdout syncBuffer
	var stderr syncBuffer
	if code := run([]string{"--say", "say", "something", "witty"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read spoken response: %v", err)
	}
	wantSpeechInput := "spoken response" + speechCompletionDirective
	if string(captured) != wantSpeechInput {
		t.Fatalf("native say received %q, want %q", string(captured), wantSpeechInput)
	}
	if stdout.String() != "" {
		t.Fatalf("voice mode unexpectedly wrote response to stdout: %q", stdout.String())
	}
}
