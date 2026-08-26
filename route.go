package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// ambiguousRouteWords is loaded from the canonical ash_bootstrap/route_words.txt.
// Shell assets embed a generated copy; keep them in sync with `make sync-route-words`.
var ambiguousRouteWords = loadAmbiguousRouteWords()

func loadAmbiguousRouteWords() []string {
	content, err := readEmbeddedBootstrapAsset("ash_bootstrap/route_words.txt")
	if err != nil {
		panic("embedded ash_bootstrap/route_words.txt is missing: " + err.Error())
	}
	words := make([]string, 0, 16)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words = append(words, line)
	}
	sort.Strings(words)
	return words
}

// renderZshRouteWords builds the generated zsh associative-array assignment.
func renderZshRouteWords() string {
	pairs := make([]string, 0, len(ambiguousRouteWords))
	for _, word := range ambiguousRouteWords {
		pairs = append(pairs, word+" 1")
	}
	return "_ash_ambiguous_words=(" + strings.Join(pairs, " ") + ")"
}

const (
	routeWordsStartMarker = "# >>> ash route words >>>"
	routeWordsEndMarker   = "# <<< ash route words <<<"
)

// applyRouteWordsBlock replaces the generated block in a shell asset with body.
func applyRouteWordsBlock(asset, body string) (string, error) {
	start := strings.Index(asset, routeWordsStartMarker)
	end := strings.Index(asset, routeWordsEndMarker)
	if start < 0 || end < 0 || end < start {
		return "", errors.New("route words markers not found")
	}
	return asset[:start] + routeWordsStartMarker + "\n" + body + "\n" + asset[end:], nil
}

// runSyncRouteWords regenerates the route-word block inside the shell source assets.
func runSyncRouteWords(stdout, stderr io.Writer) int {
	const zshAsset = "ash_bootstrap/.ash_zshrc"
	current, err := os.ReadFile(zshAsset)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	updated, err := applyRouteWordsBlock(string(current), renderZshRouteWords())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", zshAsset, err)
		return 1
	}
	if updated == string(current) {
		_, _ = fmt.Fprintf(stdout, "%s already up to date\n", zshAsset)
		return 0
	}
	// #nosec G703 -- zshAsset is a fixed repository-controlled path used only for the generated shell asset.
	if err := os.WriteFile(zshAsset, []byte(updated), 0o600); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "updated %s\n", zshAsset)
	return 0
}

var routeQuestionWords = map[string]struct{}{
	"is": {}, "are": {}, "am": {}, "do": {}, "does": {}, "did": {}, "can": {}, "could": {},
	"should": {}, "would": {}, "will": {}, "why": {}, "how": {}, "when": {}, "where": {}, "who": {},
}

// runRoute answers whether a raw command line should be routed to ash as a prompt.
// Exit code 0 means route, 1 means leave the line to the shell.
func runRoute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "--check" {
		_, _ = fmt.Fprintln(stderr, "usage: ash route --check -- <line>")
		return 2
	}
	rest := args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if shouldRoutePrompt(strings.Join(rest, " ")) {
		return 0
	}
	return 1
}

// shouldRoutePrompt applies the natural-language heuristic to a raw, unexpanded command line.
func shouldRoutePrompt(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return false
	}
	cmd := fields[0]
	args := fields[1:]
	argc := len(args)
	cmdLower := strings.ToLower(cmd)

	naturalWrapper := false
	switch cmdLower {
	case "what", "which", "who", "where", "at", "in", "for", "write":
		naturalWrapper = true
	}
	hasPathLike := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return false
		}
		if strings.Contains(arg, "/") {
			hasPathLike = true
		}
	}
	if hasPathLike && (!naturalWrapper || argc == 1) {
		return false
	}

	if cmdLower == "at" {
		firstAt := trimPromptPunctuation(strings.ToLower(args[0]))
		if strings.ContainsAny(firstAt, "0123456789:") {
			return false
		}
		switch firstAt {
		case "now", "today", "tomorrow", "teatime", "midnight", "noon", "am", "pm":
			return false
		}
	}

	switch cmd {
	case "Time", "test", "Test", "type", "Type":
		if argc == 1 && isBareToken(args[0]) {
			return false
		}
	}

	if strings.HasSuffix(args[argc-1], "?") && argc >= 2 {
		return true
	}

	first := strings.ToLower(args[0])
	if _, ok := routeQuestionWords[first]; ok && argc >= 2 {
		if !hasPathLike || (naturalWrapper && argc >= 3) {
			return true
		}
	}

	firstToken := trimPromptPunctuation(first)
	switch cmdLower {
	case "write":
		if argc >= 2 {
			switch firstToken {
			case "a", "an", "the", "this", "that", "these", "those", "my", "our", "your", "please", "poem":
				return true
			}
		}
	case "what", "which", "who", "where":
		if argc >= 3 {
			limit := 4
			if argc < limit {
				limit = argc
			}
			for i := 1; i < limit; i++ {
				token := trimPromptPunctuation(strings.ToLower(args[i]))
				if _, ok := routeQuestionWords[token]; ok {
					return true
				}
				if token == "if" {
					return true
				}
			}
		}
	case "in", "for":
		if argc >= 2 {
			switch firstToken {
			case "this", "that", "these", "those", "the", "a", "an", "my", "our", "your", "please",
				"what", "when", "how", "why", "who", "where", "is", "are", "do", "can", "should", "would":
				return true
			}
		}
	case "at":
		if argc >= 2 {
			switch firstToken {
			case "remind", "tell", "ask", "message", "note", "please", "what", "when", "how", "why", "who", "where":
				return true
			}
		}
	}

	return false
}

// trimPromptPunctuation drops a single trailing sentence punctuation character.
func trimPromptPunctuation(token string) string {
	if token == "" {
		return token
	}
	if strings.ContainsRune("?!.,:;", rune(token[len(token)-1])) {
		return token[:len(token)-1]
	}
	return token
}

func isBareToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}
