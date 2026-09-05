package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/glamour"
)

func TestStartThinkingIndicator(t *testing.T) {
	var output bytes.Buffer
	stop := startThinkingIndicator(&output)
	time.Sleep(150 * time.Millisecond)
	stop()

	got := output.String()
	if strings.Contains(got, "Thinking...") {
		t.Fatalf("expected spinner without text label, got %q", got)
	}
	if !strings.ContainsAny(got, "⠋⠙⠹⠸⠼⠴⠦⠧⠇") {
		t.Fatalf("expected braille spinner output, got %q", got)
	}
	if !strings.Contains(got, "\r") {
		t.Fatalf("expected carriage return output, got %q", got)
	}
	if strings.Contains(got, "[EID=") {
		t.Fatalf("expected thinking indicator to omit EIDs, got %q", got)
	}
}

func TestTerminalSpinnerColor(t *testing.T) {
	t.Run("dark terminal", func(t *testing.T) {
		t.Setenv("COLORFGBG", "15;0")
		t.Setenv("NO_COLOR", "")
		if got := terminalSpinnerColor(); got != "\033[97m" {
			t.Fatalf("unexpected dark-terminal color: %q", got)
		}
	})

	t.Run("light terminal", func(t *testing.T) {
		t.Setenv("COLORFGBG", "0;15")
		t.Setenv("NO_COLOR", "")
		if got := terminalSpinnerColor(); got != "\033[30m" {
			t.Fatalf("unexpected light-terminal color: %q", got)
		}
	})

	t.Run("no color disabled", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if got := terminalSpinnerColor(); got != "" {
			t.Fatalf("expected empty color when NO_COLOR is set, got %q", got)
		}
	})
}

func TestRenderMarkdownWithGlamourEmojiPassthrough(t *testing.T) {
	originalFactory := newTermRenderer
	t.Cleanup(func() { newTermRenderer = originalFactory })

	out, err := renderMarkdownWithGlamour("**bold** 🙂")
	if err != nil {
		t.Fatalf("renderMarkdownWithGlamour returned error: %v", err)
	}
	if !strings.Contains(out, "🙂") {
		t.Fatalf("expected emoji passthrough, output: %q", out)
	}
}

func TestRenderMarkdownWithGlamourFactoryError(t *testing.T) {
	originalFactory := newTermRenderer
	t.Cleanup(func() { newTermRenderer = originalFactory })

	newTermRenderer = func(...glamour.TermRendererOption) (*glamour.TermRenderer, error) {
		return nil, errors.New("factory failed")
	}

	_, err := renderMarkdownWithGlamour("x")
	if err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("expected factory failed error, got %v", err)
	}
}
