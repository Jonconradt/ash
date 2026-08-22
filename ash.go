package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	maxAgentPromptBytes               = 4096
	maxSessionIDLength                = 128
	defaultMaxToolIters               = 4
	defaultMaxAgents                  = 6
	defaultTaskMax                    = 6
	defaultRelevanceWin               = 4
	defaultStallRounds                = 2
	defaultLaunchdTimeout             = 15 * time.Second
	defaultCronTimeout                = 15 * time.Second
	defaultSchedulerLogMaxBytes int64 = 1 << 20
	defaultHistoryRetention           = 14 * 24 * time.Hour
	defaultHistoryCleanupBudget       = 300 * time.Millisecond
	defaultRetryMaxAttempts           = 3
	defaultRetryBaseDelay             = 250 * time.Millisecond
	defaultRetryMaxDelay              = 2 * time.Second
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
	childAgentEnvName                 = "ASH_CHILD_AGENT"
	childAgentEnvValue                = "1"
	maxAgentsEnvName                  = "ASH_MAX_AGENTS"
)

var sessionIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9]+`)
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)?$`)

// main is the program entry point.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run runs the requested operation.
func run(args []string, stdout, stderr io.Writer) int {
	metrics := newExecutionMetrics(timeNow())
	defer func() {
		metrics.finish(timeNow())
		if verboseLoggingEnabled() {
			_, _ = io.WriteString(stdout, renderExecutionDashboard(metrics, writerIsTerminal(stdout)))
		}
	}()

	if len(args) < 1 {
		if stdinIsInteractive() {
			printUsage(stderr)
			return 1
		}

		stdinPrompt, err := readPromptFromStdin()
		if err != nil {
			slog.Error(fmt.Sprintf("failed to read stdin prompt: %v", err), "EID", "f9hH5RjM")
			return 1
		}
		args = []string{stdinPrompt}
	}

	configureDebugLogging(stderr)
	if marker := strings.TrimSpace(os.Getenv(childAgentEnvName)); marker != "" && marker != childAgentEnvValue {
		slog.Error("invalid child-agent marker", "EID", "J9QJ8y8p")
		return 1
	}

	if args[0] == "install" {
		return runInstall(args[1:], stdout, stderr)
	}
	if args[0] == "snooze" {
		return runSnooze(args[1:], stdout, stderr)
	}

	defaultsStarted := timeNow()
	if _, err := ensureSessionID(); err != nil {
		slog.Error("failed to initialize SESSION_ID", "error", err, "EID", "xYQ5IJX7")
		return 1
	}

	configureDebugLogging()
	defer cleanupHistoryRetention(defaultHistoryRetention, defaultHistoryCleanupBudget)

	if recommendation, err := installRecommendation(); err == nil && recommendation != "" {
		slog.Info(recommendation, "EID", "Ss6EkIfE")
	}

	aiCfg, err := parseAIConfigFromEnv()
	if err != nil {
		slog.Error(fmt.Sprintf("invalid AI configuration: %v", err), "EID", "2BiYZgst")
		return 1
	}

	userInput := strings.TrimSpace(strings.Join(args, " "))
	if userInput == "" {
		slog.Info("empty input", "EID", "CPAVWywB")
		return 1
	}

	systemPrompt, err := readSystemPrompt()
	if err != nil {
		slog.Error(fmt.Sprintf("failed to read %s: %v", systemFileName, err), "EID", "8N3r3Vz0")
		return 1
	}
	systemPrompt = buildSystemPrompt(systemPrompt, timeNow())

	historyPath, err := getHistoryPath()
	if err != nil {
		slog.Error(fmt.Sprintf("failed to resolve history path: %v", err), "EID", "7rF5MhPj")
		return 1
	}

	history, err := loadHistory(historyPath)
	if err != nil {
		slog.Error(fmt.Sprintf("failed to load history: %v", err), "EID", "UxY51gAq")
		return 1
	}

	allowlist, err := loadAllowlistedCommands()
	if err != nil {
		slog.Error(fmt.Sprintf("failed to read %s: %v", toolsFileName, err), "EID", "f6qdSTFE")
		return 1
	}
	metrics.addStageDuration(metricsStageDefaults, timeNow().Sub(defaultsStarted))
	slog.Debug("Allowlist loaded", "request_id", requestIDGenerator(), "allowlist", strings.Join(sortedAllowlist(allowlist), ","), "EID", "oYccBW9V")

	toolShim := localToolShim{allowlist: allowlist, agents: newAgentBudget(maxAgents())}

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
	ctx = withExecutionMetrics(ctx, metrics)

	stopSpinner := startThinkingIndicator(stderr)
	assistantReply, updatedMessages, err := runToolLoop(ctx, aiCfg, userInput, messages, toolShim)
	stopSpinner()
	if err != nil {
		slog.Debug("run failed", "request_id", requestIDGenerator(), "error_type", fmt.Sprintf("%T", err), "error_bytes", len(err.Error()), "error_sha256", hashForLog([]byte(err.Error())), "EID", "DFr6nXH9")
		if errors.Is(err, context.Canceled) {
			slog.Info("AI doesn't feel like talking right now. Try again later.", "EID", "LAOqomnJ")
			return 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn(fmt.Sprintf("AI took longer than %s, so we should probably try again later", timeout), "EID", "80FzBwhZ")
			return 1
		}
		var statusErr chatStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusServiceUnavailable:
				slog.Warn(pickCloudBusy503Message(), "EID", "PUNKPM4h")
				return 1
			case http.StatusInternalServerError:
				slog.Warn(pickCloudServer500Message(), "EID", "Aew3mapm")
				return 1
			}
		}
		slog.Error(fmt.Sprintf("%s request failed: %v", aiCfg.Provider, err), "EID", "XflUmD5L")
		return 1
	}

	slog.Debug("assistant final reply", "request_id", requestIDGenerator(), "bytes", len(assistantReply), "sha256", hashForLog([]byte(assistantReply)), "EID", "jzszDMVF")
	fmt.Fprint(stdout, formatAssistantOutput(assistantReply))

	conversation = stripSystemMessage(updatedMessages)
	conversation = keepRecentMessages(conversation, historyLimit())
	history.Conversations[aiCfg.HistoryKey] = conversation

	if err := saveHistory(historyPath, history); err != nil {
		slog.Warn(fmt.Sprintf("warning: failed to save history: %v", err), "EID", "NIRzpBgV")
	}

	return 0
}

// printUsage writes the CLI usage text for the ash command to w.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ash <text>")
	fmt.Fprintln(w, "       ash install [--shell bash|zsh] [--dry-run] [--overwrite]")
}
