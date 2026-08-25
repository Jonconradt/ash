package main

import (
	"errors"
	"strings"
	"testing"
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
