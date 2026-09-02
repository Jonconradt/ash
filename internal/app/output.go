package app

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
)

// startThinkingIndicator starts a spinner-like indicator on w and returns a function that stops it.
func startThinkingIndicator(w io.Writer) func() {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇"}
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	color := terminalSpinnerColor()

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		frame := 0
		for {
			_, _ = fmt.Fprintf(w, "\r%s%s\033[0m", color, frames[frame])
			frame = (frame + 1) % len(frames)

			select {
			case <-done:
				_, _ = fmt.Fprint(w, "\r\033[0m\033[2K\r")
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}

func terminalSpinnerColor() string {
	if os.Getenv("NO_COLOR") != "" {
		return ""
	}

	bg := strings.TrimSpace(os.Getenv("COLORFGBG"))
	if bg == "" {
		bg = strings.TrimSpace(os.Getenv("COLOR_BG"))
	}
	if bg == "" {
		return "\033[97m"
	}
	parts := strings.Split(bg, ";")
	if len(parts) < 2 {
		return "\033[97m"
	}
	bgIndex, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || bgIndex == 0 {
		return "\033[97m"
	}
	if bgIndex >= 8 && bgIndex <= 15 {
		return "\033[30m"
	}
	return "\033[97m"
}

// renderMarkdownWithGlamour renders markdown using terminal styling for display in the CLI.
func renderMarkdownWithGlamour(markdown string) (string, error) {
	renderer, err := newTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return "", err
	}

	return renderer.Render(markdown)
}

// renderAssistantOutput styles output for terminals and preserves raw Markdown when the output is redirected or piped.
func renderAssistantOutput(raw string, terminal bool) string {
	if !terminal {
		return ensureSingleTrailingNewline(strings.TrimSpace(raw))
	}

	return formatAssistantOutput(raw)
}

// formatAssistantOutput renders assistant output for terminal display, falling back to plain text when rendering fails.
func formatAssistantOutput(raw string) string {
	rendered, err := markdownRenderer(raw)
	if err != nil {
		rendered = raw
	}

	return ensureSingleTrailingNewline(trimLeadingOutputPadding(rendered))
}

func trimLeadingOutputPadding(value string) string {
	value = strings.TrimLeft(value, "\r\n")
	if value == "" {
		return value
	}

	firstPrintable := -1
	lastStyleStart := -1
	for i := 0; i < len(value); i++ {
		if value[i] == '\x1b' && i+1 < len(value) && value[i+1] == '[' {
			j := i + 2
			for j < len(value) && value[j] != 'm' {
				j++
			}
			if j < len(value) {
				lastStyleStart = i
				i = j
				continue
			}
		}
		if value[i] == ' ' || value[i] == '\t' || value[i] == '\r' || value[i] == '\n' {
			continue
		}
		firstPrintable = i
		break
	}

	if firstPrintable == -1 {
		return ""
	}
	if lastStyleStart >= 0 && lastStyleStart < firstPrintable {
		value = value[lastStyleStart:]
	}
	return strings.TrimLeft(value, " \t\r\n")
}

// ensureSingleTrailingNewline ensures required state exists and is up to date.
func ensureSingleTrailingNewline(value string) string {
	trimmed := strings.TrimRight(value, "\n")
	return trimmed + "\n"
}
