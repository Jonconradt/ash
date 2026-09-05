package uistyle

import (
	"bytes"
	"strings"
	"testing"
)

func TestUIStyleRendering(t *testing.T) {
	var buf bytes.Buffer

	PrintMenuTitle(&buf, "Menu Title")
	if !strings.Contains(buf.String(), "Menu Title") {
		t.Errorf("expected Menu Title in output, got %q", buf.String())
	}

	buf.Reset()
	PrintHint(&buf, "Hint message")
	if !strings.Contains(buf.String(), "Hint message") {
		t.Errorf("expected Hint message in output, got %q", buf.String())
	}

	buf.Reset()
	PrintPrompt(&buf, "Prompt question")
	if !strings.Contains(buf.String(), "Prompt question") {
		t.Errorf("expected Prompt question in output, got %q", buf.String())
	}

	buf.Reset()
	PrintSuccess(&buf, "Success message")
	if !strings.Contains(buf.String(), "Success message") {
		t.Errorf("expected Success message in output, got %q", buf.String())
	}
}

func TestUIStyleAdversarialInputs(t *testing.T) {
	malformedInputs := []string{
		"",
		"\x00\x00\x00",
		strings.Repeat("A", 10000),
		"\n\n\r\t",
		"\x1b[31mRed text ANSI injection\x1b[0m",
		"%s%s%s%n",
		"🔥 Unicode test \uFFFD",
	}

	for _, input := range malformedInputs {
		var buf bytes.Buffer
		PrintMenuTitle(&buf, input)
		PrintHint(&buf, input)
		PrintPrompt(&buf, input)
		PrintSuccess(&buf, input)
	}
}
