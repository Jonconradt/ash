package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	defaultHistoryMax                 = 40
	defaultAITimeout                  = 3 * time.Minute
	defaultToolTimeout                = 15 * time.Second
	defaultToolOutputMax              = 8192
	defaultMaxToolIters               = 4
	defaultTaskMax                    = 6
	defaultRelevanceWin               = 4
	defaultStallRounds                = 2
	defaultLaunchdTimeout             = 15 * time.Second
	defaultCronTimeout                = 15 * time.Second
	defaultSchedulerLogMaxBytes int64 = 1 << 20
	defaultHistoryRetention           = 14 * 24 * time.Hour
	defaultHistoryCleanupBudget       = 300 * time.Millisecond
	sessionIDEnvName                  = "SESSION_ID"
	scheduledTaskEnvName              = "ASH_SCHEDULED_TASK"
	historyDirName                    = "history"
	systemFileName                    = ".ash_system"
	toolsFileName                     = ".ash_tools"
	ashWorkspaceDirName               = ".ash"
	inventoryFileName                 = "inventory.md"
	schedulerLogDirName               = "logs"
	launchAgentsDirName               = "launchagents"
	futurePromptAgentPrefix           = "com.user.gonetwork"
	jobMarkerPrefix                   = "# ash:job "
)

var sessionIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9]+`)

// main is the program entry point.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run runs the requested operation.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		printUsage(stderr)
		return 1
	}

	if args[0] == "install" {
		return runInstall(args[1:], stdout, stderr)
	}

	if _, err := ensureSessionID(); err != nil {
		fmt.Fprintf(stderr, "failed to initialize SESSION_ID: %v\n", err)
		return 1
	}

	configureDebugLogging()
	defer cleanupHistoryRetention(defaultHistoryRetention, defaultHistoryCleanupBudget)

	if recommendation, err := installRecommendation(); err == nil && recommendation != "" {
		fmt.Fprintln(stderr, recommendation)
	}

	aiCfg, err := parseAIConfigFromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "invalid AI configuration: %v\n", err)
		return 1
	}

	userInput := strings.TrimSpace(strings.Join(args, " "))
	if userInput == "" {
		fmt.Fprintln(stderr, "empty input")
		return 1
	}

	systemPrompt, err := readSystemPrompt()
	if err != nil {
		fmt.Fprintf(stderr, "failed to read %s: %v\n", systemFileName, err)
		return 1
	}
	systemPrompt = buildSystemPrompt(systemPrompt, timeNow())

	historyPath, err := getHistoryPath()
	if err != nil {
		fmt.Fprintf(stderr, "failed to resolve history path: %v\n", err)
		return 1
	}

	history, err := loadHistory(historyPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load history: %v\n", err)
		return 1
	}

	allowlist, err := loadAllowlistedCommands()
	if err != nil {
		fmt.Fprintf(stderr, "failed to read %s: %v\n", toolsFileName, err)
		return 1
	}
	debugLogf("Allowlist loaded: %s", strings.Join(sortedAllowlist(allowlist), ","))

	toolShim := localToolShim{allowlist: allowlist}

	conversation := history.Conversations[aiCfg.HistoryKey]
	messages := make([]message, 0, len(conversation)+2)
	messages = append(messages, message{Role: "system", Content: systemPrompt})
	messages = append(messages, conversation...)
	messages = append(messages, message{Role: "user", Content: userInput})

	timeout := aiTimeout()
	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stopSpinner := startThinkingIndicator(stderr)
	assistantReply, updatedMessages, err := runToolLoop(ctx, aiCfg, userInput, messages, toolShim)
	stopSpinner()
	if err != nil {
		debugLogf("run failed: %v", err)
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "AI doesn't feel like talking right now. Try again later.")
			return 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "AI took longer than %s, so we should probably try again later\n", timeout)
			return 1
		}
		var statusErr chatStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusServiceUnavailable:
				fmt.Fprintln(stderr, pickCloudBusy503Message())
				return 1
			case http.StatusInternalServerError:
				fmt.Fprintln(stderr, pickCloudServer500Message())
				return 1
			}
		}
		fmt.Fprintf(stderr, "ollama request failed: %v\n", err)
		return 1
	}

	debugLogf("assistant final reply: %q", assistantReply)
	fmt.Fprint(stdout, formatAssistantOutput(assistantReply))

	conversation = stripSystemMessage(updatedMessages)
	conversation = keepRecentMessages(conversation, historyLimit())
	history.Conversations[aiCfg.HistoryKey] = conversation

	if err := saveHistory(historyPath, history); err != nil {
		fmt.Fprintf(stderr, "warning: failed to save history: %v\n", err)
	}

	return 0
}

// printUsage returns the computed value for this helper.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ash <text>")
	fmt.Fprintln(w, "       ash install [--shell bash|zsh] [--dry-run]")
}
