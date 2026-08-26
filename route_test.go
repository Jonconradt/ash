package main

import (
	"sort"
	"strings"
	"testing"
)

func TestShouldRoutePrompt(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"what is the time?", true},
		{"which file is bigger?", true},
		{"who am i", true},
		{"write a poem about rain", true},
		{"in the beginning was the word", true},
		{"for the love of god explain this", true},
		{"at remind me to call", true},
		{"Tell me about Go?", true},

		{"which ls", false},
		{"who", false},
		{"test -f /etc/hosts", false},
		{"type ls", false},
		{"at 5pm", false},
		{"at now", false},
		{"which /usr/bin/env", false},
		{"write /tmp/file", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := shouldRoutePrompt(tc.line); got != tc.want {
			t.Errorf("shouldRoutePrompt(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestRunRouteExitCodes(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := runRoute([]string{"--check", "--", "what", "is", "the", "time?"}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected route exit 0 for a prompt, got %d", code)
	}
	if code := runRoute([]string{"--check", "--", "which", "ls"}, &stdout, &stderr); code != 1 {
		t.Fatalf("expected route exit 1 for a real command, got %d", code)
	}
	if code := runRoute(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("expected route exit 2 for missing --check, got %d", code)
	}
}

// The zsh widget inlines the ambiguous-word list to stay fork-free, so guard against drift.
// Regenerate with: make sync-route-words
func TestZshWidgetAmbiguousWordsMatchGo(t *testing.T) {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_zshrc")
	if err != nil {
		t.Fatalf("read embedded zsh asset: %v", err)
	}
	updated, err := applyRouteWordsBlock(string(content), renderZshRouteWords())
	if err != nil {
		t.Fatalf("apply route words block: %v", err)
	}
	if updated != string(content) {
		t.Fatal("ash_bootstrap/.ash_zshrc route words are stale; run: make sync-route-words")
	}
}

func TestAmbiguousRouteWordsLoadedFromCanonicalFile(t *testing.T) {
	if len(ambiguousRouteWords) == 0 {
		t.Fatal("expected ambiguous route words to be loaded")
	}
	if !sort.StringsAreSorted(ambiguousRouteWords) {
		t.Fatalf("expected sorted route words, got %v", ambiguousRouteWords)
	}
	for _, want := range []string{"what", "which", "time"} {
		if sort.SearchStrings(ambiguousRouteWords, want) >= len(ambiguousRouteWords) {
			t.Errorf("expected %q in route words", want)
		}
	}
}

func TestZshBootstrapAssetShape(t *testing.T) {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_zshrc")
	if err != nil {
		t.Fatalf("read embedded zsh asset: %v", err)
	}
	asset := string(content)

	for _, want := range []string{
		`[[ -n "${AI_ENDPOINT:-}" && -n "${AI_MODEL:-}" ]] || return 1`,
		`</dev/null >/dev/null 2>&1 &!`,
		"add-zsh-hook zshexit _ash_shutdown_broker",
		"zle -N accept-line _ash_accept_line",
		`[[ -o interactive ]] || return 127`,
	} {
		if !strings.Contains(asset, want) {
			t.Errorf("zsh asset missing %q", want)
		}
	}

	// Wrapper functions shadowed real builtins inside scripts; the widget replaces them.
	for _, unwanted := range []string{"_ash_route_or_delegate", `disown "`, "which() {", "test()  {"} {
		if strings.Contains(asset, unwanted) {
			t.Errorf("zsh asset still contains %q", unwanted)
		}
	}
}
