package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFormatAssistantOutputGolden(t *testing.T) {
	original := markdownRenderer
	t.Cleanup(func() { markdownRenderer = original })

	markdownRenderer = func(string) (string, error) {
		return "\x1b[1mbold 🙂\x1b[0m\n\n", nil
	}

	got := formatAssistantOutput("ignored")
	gotQuoted := strconv.QuoteToASCII(got) + "\n"

	goldenPath := filepath.Join("testdata", "formatAssistantOutput.golden")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}

	if string(want) != gotQuoted {
		t.Fatalf("golden mismatch\nwant: %s\ngot:  %s", strings.TrimSpace(string(want)), strings.TrimSpace(gotQuoted))
	}
}
