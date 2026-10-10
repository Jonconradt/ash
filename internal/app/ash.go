package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"ash/internal/localize"
	"ash/internal/scratchlife"
	"ash/internal/workspace"
)

var (
	ashVersion          = "dev"
	ashCommit           = "unknown"
	ashDevelopmentBuild = "false"
)

const (
	defaultHistoryMax                 = 40
	defaultAITimeout                  = 3 * time.Minute
	defaultToolTimeout                = 15 * time.Second
	defaultToolOutputMax              = 8192
	maxAgentPromptBytes               = 4096
	maxSessionIDLength                = 128
	defaultMaxToolIters               = 16
	defaultMaxAgents                  = 6
	defaultTaskMax                    = 6
	defaultRelevanceWin               = 4
	defaultStallRounds                = 2
	defaultToolRepeatLimit            = 2
	defaultLaunchdTimeout             = 15 * time.Second
	defaultCronTimeout                = 15 * time.Second
	defaultSchedulerLogMaxBytes int64 = 1 << 20
	defaultHistoryRetention           = 14 * 24 * time.Hour
	defaultHistoryCleanupBudget       = 300 * time.Millisecond
	defaultRetryMaxAttempts           = 3
	defaultRetryBaseDelay             = 250 * time.Millisecond
	defaultRetryMaxDelay              = 2 * time.Second
	scratchDirName                    = "scratch"
	scratchAccessFileName             = ".ash_scratch_access"
	scratchCleanupMaxAge              = 48 * time.Hour
	scratchCleanupIdleAge             = 24 * time.Hour
	sessionIDEnvName                  = "SESSION_ID"
	scheduledTaskEnvName              = "ASH_SCHEDULED_TASK"
	historyDirName                    = "history"
	runtimeDirName                    = "runtime"
	brokerSocketProbeTimeout          = 50 * time.Millisecond
	systemFileName                    = ".ash_system"
	allowFileName                     = ".ash_allow"
	denyFileName                      = ".ash_deny"
	legacyToolsFileName               = ".ash_tools"
	ashWorkspaceDirName               = workspace.DirName
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

// Run executes the ash CLI for the given args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr)
}

// run runs the requested operation.
func run(args []string, stdout, stderr io.Writer) int {
	if !initLanguage(stderr) {
		return 1
	}
	voiceMode := len(args) > 0 && args[0] == "--say" && !speechTextOutputEnabled()
	if voiceMode {
		args = args[1:]
	}
	metrics := newExecutionMetrics(timeNow())
	summaryRequestID := ""
	defer func() {
		metrics.finish(timeNow())
		if verboseLoggingEnabled() && !voiceMode {
			logExecutionSummary(summaryRequestID, metrics)
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
			slog.Error(localize.Format("log.ash.stdin_read_failed", []any{err}), "EID", "f9hH5RjM")
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
	if args[0] == "mcp" {
		return runMCP(args[1:], stdout, stderr)
	}
	if args[0] == "snooze" {
		return runSnooze(args[1:], stdout, stderr)
	}
	if args[0] == "broker" {
		_, _ = fmt.Fprintln(stderr, localize.Text("ash.broker_moved"))
		return 1
	}
	if args[0] == "route" {
		return runRoute(args[1:], stdout, stderr)
	}
	if args[0] == "update" {
		return runUpgrade(args[1:], stdout, stderr)
	}
	if args[0] == "--internal-export-assets" {
		return runUpgradeAssetExport(args[1:], stdout, stderr)
	}
	if args[0] == "--internal-sync-route-words" {
		return runSyncRouteWords(stdout, stderr)
	}
	if args[0] == "--internal-provision-python" {
		provisionManagedPythonEnv(stdout)
		return 0
	}

	args, attachPaths, err := parseAttachFlags(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", err)
		return 1
	}
	attachments, err := loadAttachments(attachPaths)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, localize.Format("ash.attachment_error", []any{err}))
		return 1
	}

	defaultsStarted := timeNow()
	sessionID, err := ensureSessionID()
	if err != nil {
		slog.Error("failed to initialize SESSION_ID", "error", err, "EID", "xYQ5IJX7")
		return 1
	}

	configureDebugLogging()
	defer cleanupWorkspaceRetention(defaultHistoryRetention, defaultHistoryCleanupBudget)
	scratchRoot, err := ashScratchRoot()
	if err != nil {
		slog.Error(localize.Format("log.scratch.lifecycle_failed", []any{err}), "error", err, "EID", "6CK2vCl2")
		return 1
	}
	scratchLock, err := scratchlife.Acquire(scratchRoot, sessionID)
	if err != nil {
		slog.Error(localize.Format("log.scratch.lifecycle_failed", []any{err}), "error", err, "EID", "AXhiG6LH")
		return 1
	}
	defer func() {
		if err := scratchLock.Close(); err != nil {
			slog.Warn(localize.Format("log.scratch.lifecycle_failed", []any{err}), "error", err, "EID", "Pi5Vn73F")
		}
		if _, err := cleanupStaleScratchDirs(scratchRoot, timeNow()); err != nil {
			slog.Debug(localize.Format("log.scratch.lifecycle_failed", []any{err}), "error", err, "EID", "VXWZusLT")
		}
	}()

	aiCfg, err := parseAIConfigFromEnv()
	if err != nil {
		_, _ = fmt.Fprint(stderr, localize.Format("ash.configuration_error", []any{err}))
		return 1
	}
	slog.Debug("ash session started", "session_id", sessionID, "version", executionDashboardVersion(), "provider", aiCfg.Provider, "ollama_openai_api", aiCfg.OllamaOpenAIAPI, "stream_requested", streamingEnabled(), "EID", "vN2wSb8Q")

	if recommendation, err := installRecommendation(); err == nil && recommendation != "" {
		slog.Info(localize.LogMessage(recommendation), "EID", "Ss6EkIfE")
	}

	userInput := strings.TrimSpace(strings.Join(args, " "))
	if userInput == "" {
		slog.Info("empty input", "EID", "CPAVWywB")
		return 1
	}

	historyPath, err := getHistoryPath()
	if err != nil {
		slog.Error(localize.Format("log.ash.history_path_failed", []any{err}), "EID", "7rF5MhPj")
		return 1
	}

	history, err := loadHistory(historyPath)
	if err != nil {
		slog.Error(localize.Format("log.ash.history_load_failed", []any{err}), "EID", "UxY51gAq")
		return 1
	}

	allowlist, err := loadAllowlistedCommands()
	if err != nil {
		slog.Error(localize.Format("log.ash.file_read_failed", []any{allowFileName, err}), "EID", "f6qdSTFE")
		return 1
	}

	systemPrompt, err := readSystemPromptWithAllowlist(allowlist)
	if err != nil {
		slog.Error(localize.Format("log.ash.file_read_failed", []any{systemFileName, err}), "EID", "8N3r3Vz0")
		return 1
	}
	systemPrompt = buildSystemPrompt(systemPrompt, timeNow())

	requestID := requestIDGenerator()
	summaryRequestID = requestID
	metrics.addStageDuration(metricsStageDefaults, timeNow().Sub(defaultsStarted))
	slog.Debug("Allowlist loaded", "request_id", requestID, "allowlist", strings.Join(sortedAllowlist(allowlist), ","), "EID", "oYccBW9V")

	localShim := localToolShim{allowlist: allowlist, agents: newAgentBudget(maxAgents())}

	conversation := history.Conversations[aiCfg.HistoryKey]
	messages := make([]message, 0, len(conversation)+2)
	messages = append(messages, message{Role: "system", Content: systemPrompt})
	messages = append(messages, conversation...)
	messages = append(messages, message{Role: "user", Content: userInput, Attachments: attachments})

	timeout := aiTimeout()
	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = withExecutionMetrics(ctx, metrics)
	ctx = withRequestID(ctx, requestID)
	toolShim := prepareTools(ctx, stderr, localShim, stdinIsInteractive())

	stopSpinner := startThinkingIndicator(stderr)
	assistantReply, updatedMessages, err := runToolLoop(ctx, aiCfg, userInput, messages, toolShim)
	stopSpinner()
	if err != nil {
		slog.Debug("run failed", "request_id", requestIDFromContext(ctx), "error_type", fmt.Sprintf("%T", err), "error_bytes", len(err.Error()), "error_sha256", hashForLog([]byte(err.Error())), "EID", "DFr6nXH9")
		if errors.Is(err, context.Canceled) {
			slog.Info("AI doesn't feel like talking right now. Try again later.", "EID", "LAOqomnJ")
			return 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn(localize.Format("log.ash.timeout", []any{timeout}), "EID", "80FzBwhZ")
			return 1
		}
		var statusErr chatStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusServiceUnavailable:
				slog.Warn(withStatusDetail(pickCloudBusy503Message(), statusErr), "EID", "PUNKPM4h")
				return 1
			case http.StatusInternalServerError:
				slog.Warn(withStatusDetail(pickCloudServer500Message(), statusErr), "EID", "Aew3mapm")
				return 1
			case http.StatusTooManyRequests:
				slog.Warn(withStatusDetail(pickCloudRateLimit429Message(), statusErr), "EID", "Qm7VtLx2")
				return 1
			}
		}
		slog.Error(localize.Format("log.ash.provider_request_failed", []any{aiCfg.Provider, err}), "EID", "XflUmD5L")
		return 1
	}

	slog.Debug("assistant final reply", "request_id", requestIDFromContext(ctx), "bytes", len(assistantReply), "sha256", hashForLog([]byte(assistantReply)), "EID", "jzszDMVF")
	if voiceMode {
		spoken, speakErr := speakAssistantReply(ctx, assistantReply, stdout, stderr)
		if speakErr != nil {
			_, _ = fmt.Fprint(stderr, localize.Format("ash.say_failed", []any{speakErr}))
			return 1
		}
		if !spoken {
			_, _ = fmt.Fprintln(stderr, localize.Text("ash.say_unavailable"))
			_, _ = fmt.Fprint(stdout, renderAssistantOutput(assistantReply, writerIsTerminal(stdout)))
		}
	} else {
		_, _ = fmt.Fprint(stdout, renderAssistantOutput(assistantReply, writerIsTerminal(stdout)))
	}

	if replyAttachments := finalAssistantAttachments(updatedMessages); len(replyAttachments) > 0 {
		if scratchRoot, scratchErr := ashScratchRoot(); scratchErr == nil {
			outDir := filepath.Join(scratchRoot, "attachments", requestID)
			if written, writeErr := writeResponseAttachments(outDir, replyAttachments); writeErr != nil {
				slog.Warn(localize.Format("log.ash.attachment_save_failed", []any{writeErr}), "EID", "b3Kx9Qmz")
			} else {
				for _, path := range written {
					attachmentOutput := stdout
					if voiceMode {
						attachmentOutput = stderr
					}
					_, _ = fmt.Fprint(attachmentOutput, localize.Format("ash.saved_attachment", []any{path}))
				}
			}
		}
	}

	conversation = stripSystemMessage(updatedMessages)
	conversation = keepRecentMessages(conversation, historyLimit())
	history.Conversations[aiCfg.HistoryKey] = conversation

	if err := saveHistory(historyPath, history); err != nil {
		slog.Warn(localize.Format("log.ash.history_save_failed", []any{err}), "EID", "NIRzpBgV")
	}

	return 0
}

// printUsage writes the CLI usage text for the ash command to w.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, localize.Text("usage.ash.prompt"))
	_, _ = fmt.Fprintln(w, localize.Text("usage.ash.install"))
	_, _ = fmt.Fprintln(w, localize.Text("usage.ash.mcp"))
	_, _ = fmt.Fprintln(w, localize.Text("usage.ash.update"))
	_, _ = fmt.Fprintln(w, localize.Text("usage.ash.broker"))
}
