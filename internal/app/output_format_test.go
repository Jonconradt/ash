package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
)

func TestEnsureSingleTrailingNewline(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "\n"},
		{name: "no newline", in: "hello", want: "hello\n"},
		{name: "one newline", in: "hello\n", want: "hello\n"},
		{name: "many newlines", in: "hello\n\n", want: "hello\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureSingleTrailingNewline(tt.in)
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestFormatAssistantOutputUsesRenderer(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(input string) (string, error) {
		if input != "# title" {
			t.Fatalf("unexpected renderer input: %q", input)
		}
		return "styled\n\n", nil
	}

	got := formatAssistantOutput("# title")
	if got != "styled\n" {
		t.Fatalf("output mismatch: got %q want %q", got, "styled\\n")
	}
}

func TestRenderAssistantOutput(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(string) (string, error) {
		return "  • styled\n\n", nil
	}

	if got := renderAssistantOutput("\n- raw\n\n", false); got != "- raw\n" {
		t.Fatalf("non-terminal output should stay raw markdown, got %q", got)
	}
	if got := renderAssistantOutput("- raw", true); got != "• styled\n" {
		t.Fatalf("terminal output should be rendered, got %q", got)
	}
}

func TestRenderAssistantOutputStylesForAnsiTerminal(t *testing.T) {
	originalFactory := newTermRenderer
	t.Cleanup(func() { newTermRenderer = originalFactory })

	// Tests run without a TTY, so glamour would otherwise fall back to its notty style.
	newTermRenderer = func(options ...glamour.TermRendererOption) (*glamour.TermRenderer, error) {
		return originalFactory(append(options, glamour.WithStandardStyle("dark"), glamour.WithColorProfile(termenv.TrueColor))...)
	}

	got := renderAssistantOutput("## Features\n\n- alpha\n", true)
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI styling for an interactive terminal, got %q", got)
	}
	if !strings.Contains(got, "Features") || !strings.Contains(got, "alpha") {
		t.Fatalf("expected rendered content, got %q", got)
	}
}

func TestFormatAssistantOutputFallbackOnRendererError(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(string) (string, error) {
		return "", errors.New("boom")
	}

	got := formatAssistantOutput("**raw** 🙂")
	want := "**raw** 🙂\n"
	if got != want {
		t.Fatalf("fallback mismatch: got %q want %q", got, want)
	}
}

func TestFormatAssistantOutputTrimsLeadingBlankLineAndIndent(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(input string) (string, error) {
		if input != "what time is it?" {
			t.Fatalf("unexpected renderer input: %q", input)
		}
		return "\n\x1b[38;5;252m\x1b[0m\x1b[38;5;252m\x1b[0m  \x1b[38;5;252mIt is 12:17 PM EDT on Sunday, August 23, 2026.\x1b[0m\n\n", nil
	}

	got := formatAssistantOutput("what time is it?")
	if strings.HasPrefix(got, "\n") || strings.HasPrefix(got, "  ") {
		t.Fatalf("output still has leading blank line or indentation: %q", got)
	}
	if !strings.Contains(got, "It is 12:17 PM EDT on Sunday, August 23, 2026.") {
		t.Fatalf("output missing rendered text: %q", got)
	}
}
