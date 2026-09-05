package app

import (
	"regexp"
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

var conservativeOperandPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func shouldRouteToAshConservative(command string, args []string) bool {
	// Rule A: no args => delegate.
	if len(args) == 0 {
		return false
	}

	// Rule B: flag-style args => delegate.
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return false
		}
	}

	cmdLower := strings.ToLower(command)
	naturalWrapper := cmdLower == "what" || cmdLower == "which" || cmdLower == "who" || cmdLower == "where" || cmdLower == "in" || cmdLower == "for"
	hasPathLike := false

	// Rule C: path-like args generally delegate, except natural-language wrapper
	// prompts with multiple tokens may still route via Rule F2.
	for _, arg := range args {
		if strings.Contains(arg, "/") || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") {
			hasPathLike = true
			break
		}
	}
	if hasPathLike && (!naturalWrapper || len(args) == 1) {
		return false
	}

	if cmdLower == "at" {
		firstAt := strings.ToLower(strings.Trim(args[0], "?!.:,;"))
		if strings.ContainsAny(firstAt, "0123456789:") {
			return false
		}
		switch firstAt {
		case "now", "today", "tomorrow", "teatime", "midnight", "noon", "am", "pm":
			return false
		}
	}

	// Rule D: builtin/keyword single-operand forms => delegate.
	switch strings.ToLower(command) {
	case "time", "test", "type":
		if len(args) == 1 && conservativeOperandPattern.MatchString(args[0]) {
			return false
		}
	}

	full := command
	if len(args) > 0 {
		full += " " + strings.Join(args, " ")
	}

	// Rule E: trailing question mark with enough tokens => ash.
	if strings.HasSuffix(full, "?") && len(args) >= 2 {
		return true
	}

	// Rule F: interrogative/auxiliary first arg with enough tokens => ash.
	first := strings.ToLower(args[0])
	switch first {
	case "is", "are", "am", "do", "does", "did", "can", "could", "should", "would", "will", "why", "how", "when", "where", "who":
		if !hasPathLike || (naturalWrapper && len(args) >= 3) {
			return len(args) >= 2
		}
	}

	// Rule F2: for natural-language wrappers, allow early auxiliary/interrogative
	// tokens beyond the first word (for example "What directory am I in ...").
	switch cmdLower {
	case "what", "which", "who", "where":
		if len(args) >= 3 {
			limit := 4
			if len(args) < limit {
				limit = len(args)
			}
			for i := 1; i < limit; i++ {
				token := strings.ToLower(strings.Trim(args[i], "?!.:,;"))
				switch token {
				case "is", "are", "am", "do", "does", "did", "can", "could", "should", "would", "will", "why", "how", "when", "where", "who", "if":
					return true
				}
			}
		}
	case "in", "for":
		if len(args) >= 2 {
			firstToken := strings.ToLower(strings.Trim(args[0], "?!.:,;"))
			switch firstToken {
			case "this", "that", "these", "those", "the", "a", "an", "my", "our", "your", "please", "what", "when", "how", "why", "who", "where", "is", "are", "do", "can", "should", "would":
				return true
			}
		}
	case "at":
		if len(args) >= 2 {
			firstToken := strings.ToLower(strings.Trim(args[0], "?!.:,;"))
			switch firstToken {
			case "remind", "tell", "ask", "message", "note", "please", "what", "when", "how", "why", "who", "where":
				return true
			}
		}
	}

	// Rule G: default => delegate.
	return false
}

func TestShouldRouteToAshConservative(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{name: "rule A no args", command: "what", args: nil, want: false},
		{name: "rule B flag arg", command: "what", args: []string{"-s", "file"}, want: false},
		{name: "rule C path arg", command: "what", args: []string{"/usr/bin/what"}, want: false},
		{name: "rule D builtin single operand", command: "test", args: []string{"foo"}, want: false},
		{name: "rule E trailing question", command: "What", args: []string{"time", "is", "it?"}, want: true},
		{name: "rule F interrogative first arg", command: "what", args: []string{"is", "awk"}, want: true},
		{name: "rule F2 natural language mid auxiliary", command: "What", args: []string{"directory", "am", "I", "in", "and", "are", "there", "any", "executeable", "files", "Run", "multiple", "tools", "if", "necessary"}, want: true},
		{name: "rule F2 natural language with path token", command: "what", args: []string{"time", "is", "it", "and", "list", "all", "files", "in", "~/.ash/logs"}, want: true},
		{name: "rule F interrogative with path token for who", command: "who", args: []string{"am", "I", "and", "list", "files", "in", "~/.ash/logs"}, want: true},
		{name: "rule in natural prompt routed", command: "In", args: []string{"this", "repo", "what", "files", "changed"}, want: true},
		{name: "rule for natural prompt routed", command: "For", args: []string{"this", "error", "what", "should", "I", "do"}, want: true},
		{name: "rule at natural prompt routed", command: "at", args: []string{"remind", "me", "tomorrow"}, want: true},
		{name: "rule at scheduler time delegates", command: "at", args: []string{"5pm"}, want: false},
		{name: "rule at now delegates", command: "at", args: []string{"now", "+", "1", "minute"}, want: false},
		{name: "rule G default delegate", command: "which", args: []string{"ls"}, want: false},
		{name: "precedence B over E", command: "what", args: []string{"-n", "what?"}, want: false},
		{name: "precedence C over F", command: "what", args: []string{"who", "./path"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRouteToAshConservative(tt.command, tt.args); got != tt.want {
				t.Fatalf("route decision mismatch: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestBashRoutingPolicy(t *testing.T) {
	// bash guards defer to shouldRoutePrompt via `ash route --check`; see bash_route_test.go
	// for the guard-equivalence and fork-avoidance tests.
	tests := []struct {
		name       string
		invocation string
		want       bool
	}{
		{name: "title case what routed", invocation: "What time is it?", want: true},
		{name: "lower case what routed", invocation: "what time is it?", want: true},
		{name: "write natural language routed", invocation: "write a poem using the following as inspiration love.txt", want: true},
		{name: "write command form delegates", invocation: "write user", want: false},
		{name: "what mid auxiliary routed", invocation: "What directory am I in and are there any executeable files", want: true},
		{name: "what sentence with path routed", invocation: "what time is it and list all of the files in the ~/.ash/logs", want: true},
		{name: "what path delegates", invocation: "what /usr/bin/what", want: false},
		{name: "what flag delegates", invocation: "what -a", want: false},
		{name: "title case time routed", invocation: "Time is it late?", want: true},
		{name: "test question routed", invocation: "test is this thing on?", want: true},
		{name: "test flag delegates", invocation: "test -f /etc/hosts", want: false},
		{name: "type question routed", invocation: "type is this a question?", want: true},
		{name: "type command form delegates", invocation: "type ls", want: false},
		{name: "which question routed", invocation: "which file is bigger?", want: true},
		{name: "which command form delegates", invocation: "which ls", want: false},
		{name: "who question routed", invocation: "who am i", want: true},
		{name: "who no args delegates", invocation: "who", want: false},
		{name: "In title case routed", invocation: "In this repo what files changed", want: true},
		{name: "For title case routed", invocation: "For this error what should I do", want: true},
		{name: "at natural routed", invocation: "at remind me tomorrow", want: true},
		{name: "at scheduler delegates", invocation: "at 5pm", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRoutePrompt(tt.invocation); got != tt.want {
				t.Fatalf("shouldRoutePrompt(%q) = %v, want %v", tt.invocation, got, tt.want)
			}
		})
	}
}

func TestZshRoutingPolicy(t *testing.T) {
	// zsh routes through the ZLE widget, which delegates the decision to shouldRoutePrompt.
	tests := []struct {
		name       string
		invocation string
		want       bool
	}{
		{name: "title case what routed", invocation: "What time is it?", want: true},
		{name: "lower case what routed", invocation: "what time is it?", want: true},
		{name: "write natural language routed", invocation: "write a poem using the following as inspiration love.txt", want: true},
		{name: "write command form delegates", invocation: "write user", want: false},
		{name: "what mid auxiliary routed", invocation: "What directory am I in and are there any executeable files Run multiple tools if necessary", want: true},
		{name: "what sentence with path routed", invocation: "what time is it and list all of the files in the ~/.ash/logs", want: true},
		{name: "what path delegates", invocation: "what /usr/bin/what", want: false},
		{name: "title case time routed", invocation: "Time is it late?", want: true},
		{name: "where question routed", invocation: "where should logs go", want: true},
		{name: "where command form delegates", invocation: "where ls", want: false},
		{name: "who with path routed", invocation: "who am I and list files in ~/.ash/logs", want: true},
		{name: "In title case routed", invocation: "In this repo what files changed", want: true},
		{name: "For title case routed", invocation: "For this error what should I do", want: true},
		{name: "for loop unchanged", invocation: "for x in a b; do echo $x; done", want: false},
		{name: "at natural routed", invocation: "at remind me tomorrow", want: true},
		{name: "at scheduler delegates", invocation: "at 5pm", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRoutePrompt(tt.invocation); got != tt.want {
				t.Fatalf("shouldRoutePrompt(%q) = %v, want %v", tt.invocation, got, tt.want)
			}
		})
	}
}
