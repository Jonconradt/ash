package app

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
		{"say something witty", true},
		{"may I use your API", true},
		{"might this cause issues", true},
		{"must we deploy now", true},
		{"shall we continue", true},
		{"ought we to wait", true},
		{"whom should I ask", true},
		{"whose file is this", true},
		{"whence did this custom begin", true},
		{"whither should we go", true},
		{"what's the weather", true},
		{"where's my file", true},
		{"Tell me about Go?", true},

		{"which ls", false},
		{"who", false},
		{"test -f /etc/hosts", false},
		{"type ls", false},
		{"at 5pm", false},
		{"at now", false},
		{"say -v Alex hello", false},
		{"say --version", false},
		{"say", false},
		{"may -v", false},
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
	for _, want := range []string{"say", "what", "which", "time"} {
		if sort.SearchStrings(ambiguousRouteWords, want) >= len(ambiguousRouteWords) {
			t.Errorf("expected %q in route words", want)
		}
	}
}

func TestFishSayRoutingPolicyMatchesGo(t *testing.T) {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/.ash_fish.fish")
	if err != nil {
		t.Fatalf("read embedded fish asset: %v", err)
	}
	asset := string(content)
	for _, want := range []string{
		"case say",
		"case out something a an the please why how when where who what can could should would",
		"function say; _ash_route_or_delegate_say say $argv; end",
	} {
		if !strings.Contains(asset, want) {
			t.Errorf("fish asset missing say routing fragment %q", want)
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
		`--parent-pid "$parent_pid"`,
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

func TestUnixShellBootstrapAssetsPassParentPID(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "ash_bootstrap/.ash_bashrc", want: `local parent_pid="$BASHPID"`},
		{path: "ash_bootstrap/.ash_fish.fish", want: "set -l parent_pid $fish_pid"},
	}
	for _, tc := range cases {
		content, err := readEmbeddedBootstrapAsset(tc.path)
		if err != nil {
			t.Fatalf("read embedded asset %q: %v", tc.path, err)
		}
		if !strings.Contains(string(content), tc.want) || !strings.Contains(string(content), `--parent-pid "$parent_pid"`) {
			t.Errorf("bootstrap asset %q does not pass its parent PID", tc.path)
		}
	}
}
