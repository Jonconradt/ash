package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
)

type historyData struct {
	Conversations map[string][]message `json:"conversations"`
}

type toolCommandResult struct {
	OK       bool   `json:"ok"`
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
}

type recurringJobMetadata struct {
	ID        string            `json:"id"`
	Cron      string            `json:"cron"`
	Prompt    string            `json:"prompt"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	Purpose   string            `json:"purpose,omitempty"`
	CreatedAt string            `json:"created_at"`
}

type recurringJobRecord struct {
	Meta    recurringJobMetadata `json:"meta"`
	Line    string               `json:"line"`
	Command string               `json:"command"`
}

var (
	markdownRenderer    = renderMarkdownWithGlamour
	osGetwd             = os.Getwd
	osUserHomeDir       = os.UserHomeDir
	osReadFile          = os.ReadFile
	osWriteFile         = os.WriteFile
	execLookPath        = exec.LookPath
	execCommandOutput   = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).Output() }
	execCommandContext  = exec.CommandContext
	osMkdirAll          = os.MkdirAll
	osExecutable        = os.Executable
	timeNow             = time.Now
	newTermRenderer     = glamour.NewTermRenderer
	signalNotifyContext = signal.NotifyContext
	newHTTPClient       = func(timeout time.Duration) *http.Client {
		return &http.Client{Timeout: timeout}
	}
	argumentBlockPattern                = regexp.MustCompile(`(;|\|\||&&|\||` + "`" + `|\$\(|>|<|\x00|\n|\r)`)
	toolCommandRunner                   = runToolCommand
	pickCloudBusy503Message             = randomCloudBusy503Message
	pickCloudServer500Message           = randomCloudServer500Message
	debugWriter               io.Writer = os.Stderr
	debugJSONLogging          bool
	requestIDGenerator        func() string
)

func init() {
	requestIDGenerator = func() string {
		buf := make([]byte, 8)
		_, _ = rand.Read(buf)
		return hex.EncodeToString(buf)
	}
}

// verboseLoggingEnabled returns the computed value for this helper.
func verboseLoggingEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("ASH_VERBOSE")))
	switch raw {
	case "1", "true", "yes", "on", "debug":
		return true
	default:
		return false
	}
}

// configureDebugLogging returns the computed value for this helper.
func configureDebugLogging() {
	debugWriter = os.Stderr
	debugJSONLogging = false

	if !verboseLoggingEnabled() {
		return
	}

	logFile := strings.TrimSpace(os.Getenv("ASH_LOG_FILE"))
	if logFile == "" {
		computedPath, err := schedulerLogFilePath(false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		logFile = computedPath
	}

	maxBytes := defaultSchedulerLogMaxBytes
	if raw := strings.TrimSpace(os.Getenv("ASH_LOG_MAX_BYTES")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			maxBytes = parsed
		}
	}

	writer, err := newRotatingSchedulerLogWriter(logFile, maxBytes)
	if err != nil {
		return
	}

	debugWriter = writer
	debugJSONLogging = strings.EqualFold(strings.TrimSpace(os.Getenv("ASH_LOG_FORMAT")), "json")
}

// debugLogf returns the computed value for this helper.
func debugLogf(format string, args ...any) {
	if !verboseLoggingEnabled() {
		return
	}
	if debugWriter == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	if debugJSONLogging {
		record := map[string]any{
			"time":       timeNow().UTC().Format(time.RFC3339Nano),
			"level":      "debug",
			"message":    message,
			"request_id": requestIDGenerator(),
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			_, _ = fmt.Fprintf(debugWriter, "[ash-debug] %s\n", sanitizeJSONError(err.Error()))
			return
		}
		_, _ = debugWriter.Write(append(encoded, '\n'))
		return
	}
	_, _ = fmt.Fprintf(debugWriter, "[ash-debug] %s\n", message)
}

type rotatingSchedulerLogWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

// newRotatingSchedulerLogWriter returns the computed value for this helper.
func newRotatingSchedulerLogWriter(path string, maxBytes int64) (*rotatingSchedulerLogWriter, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("log file path must be a non-empty string")
	}
	if maxBytes <= 0 {
		maxBytes = defaultSchedulerLogMaxBytes
	}
	if err := osMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	writer := &rotatingSchedulerLogWriter{path: path, maxBytes: maxBytes}
	if err := writer.openCurrent(); err != nil {
		return nil, err
	}
	return writer, nil
}

// Write writes data to the current log file, rotating when needed.
func (w *rotatingSchedulerLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.openCurrent(); err != nil {
			return 0, err
		}
	}

	if w.maxBytes > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateCurrent(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// openCurrent opens the current log file handle.
func (w *rotatingSchedulerLogWriter) openCurrent() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	if w.maxBytes > 0 && w.size >= w.maxBytes {
		return w.rotateCurrent()
	}
	return nil
}

// rotateCurrent rotates the current log file.
func (w *rotatingSchedulerLogWriter) rotateCurrent() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	backupPath := w.path + ".1"
	_ = os.Remove(backupPath)
	if _, err := os.Stat(w.path); err == nil {
		if err := os.Rename(w.path, backupPath); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}

// marshalForDebug marshals the value for debug logging.
func marshalForDebug(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<marshal-error:%s>", sanitizeJSONError(err.Error()))
	}
	return string(encoded)
}

// sortedAllowlist returns a sorted copy of the input values.
func sortedAllowlist(allowlist map[string]struct{}) []string {
	out := make([]string, 0, len(allowlist))
	for name := range allowlist {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// stripSystemMessage removes the leading system message when present.
func stripSystemMessage(messages []message) []message {
	if len(messages) == 0 {
		return nil
	}
	if messages[0].Role == "system" {
		return append([]message(nil), messages[1:]...)
	}
	return append([]message(nil), messages...)
}

// getHistoryPath returns the computed value for this helper.
func getHistoryPath() (string, error) {
	if _, err := ensureSessionID(); err != nil {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	historyDir := filepath.Join(home, ashWorkspaceDirName, historyDirName)
	if err := osMkdirAll(historyDir, 0o700); err != nil {
		return "", err
	}

	sessionID, err := sanitizedSessionIDForLogFile()
	if err != nil {
		return "", err
	}

	filename := sessionID + ".json"
	if isScheduledTaskRun() {
		filename = "task_" + sessionID + ".json"
	}

	return filepath.Join(historyDir, filename), nil
}

// isScheduledTaskRun reports whether the condition is true.
func isScheduledTaskRun() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(scheduledTaskEnvName)))
	return raw == "1" || raw == "true" || raw == "yes"
}

// cleanupHistoryRetention returns the computed value for this helper.
func cleanupHistoryRetention(maxAge, budget time.Duration) {
	if maxAge <= 0 || budget <= 0 {
		return
	}
	home, err := osUserHomeDir()
	if err != nil {
		return
	}
	historyDir := filepath.Join(home, ashWorkspaceDirName, historyDirName)
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return
	}

	deadline := timeNow().Add(budget)
	cutoff := timeNow().Add(-maxAge)
	for _, entry := range entries {
		if timeNow().After(deadline) {
			return
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(historyDir, entry.Name()))
	}
}

// loadHistory loads data from storage.
func loadHistory(path string) (historyData, error) {
	content, err := osReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return historyData{Conversations: map[string][]message{}}, nil
	}
	if err != nil {
		return historyData{}, err
	}

	var data historyData
	if err := json.Unmarshal(content, &data); err != nil {
		return historyData{}, err
	}
	if data.Conversations == nil {
		data.Conversations = map[string][]message{}
	}

	return data, nil
}

// saveHistory saves data to storage.
func saveHistory(path string, data historyData) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return osWriteFile(path, content, 0o600)
}

// buildSystemPrompt builds and returns a derived value.
func buildSystemPrompt(userPrompt string, now time.Time) string {
	header := fmt.Sprintf("Current local datetime: %s", now.Format(time.RFC3339))
	trimmed := strings.TrimSpace(userPrompt)
	if trimmed == "" {
		return header
	}
	return header + "\n\n" + trimmed
}

// schedulerEnvAllowlist returns the computed value for this helper.
func schedulerEnvAllowlist() map[string]string {
	keys := []string{
		aiEnvEndpoint,
		aiEnvModel,
		aiEnvAuthType,
		aiEnvAuthToken,
		sessionIDEnvName,
		scheduledTaskEnvName,
		"HOME",
		"PATH",
		"AI_TIMEOUT",
		"ASH_HISTORY_MAX",
		"ASH_VERBOSE",
		"ASH_LOG_FILE",
		"ASH_LOG_FORMAT",
		"ASH_LOG_MAX_BYTES",
		"ASH_TOOL_ALLOWLIST",
		"ASH_TOOL_TIMEOUT",
		"ASH_TOOL_OUTPUT_MAX",
		"ASH_MAX_TOOL_ITERS",
	}
	out := map[string]string{}
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		out[key] = value
	}
	return out
}

// schedulerInvocationEnv returns the computed value for this helper.
func schedulerInvocationEnv() map[string]string {
	env := schedulerEnvAllowlist()
	if strings.TrimSpace(env[sessionIDEnvName]) == "" {
		if generated, err := generateSessionID(); err == nil {
			env[sessionIDEnvName] = generated
		}
	}
	env[scheduledTaskEnvName] = "1"
	if strings.TrimSpace(env["ASH_VERBOSE"]) == "" {
		env["ASH_VERBOSE"] = "1"
	}
	if strings.TrimSpace(env["ASH_LOG_FILE"]) == "" {
		if logFile, err := schedulerLogFilePathForSession(env[sessionIDEnvName], true); err == nil {
			env["ASH_LOG_FILE"] = logFile
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	if strings.TrimSpace(env["ASH_LOG_FORMAT"]) == "" {
		env["ASH_LOG_FORMAT"] = "json"
	}
	if strings.TrimSpace(env["ASH_LOG_MAX_BYTES"]) == "" {
		env["ASH_LOG_MAX_BYTES"] = strconv.FormatInt(defaultSchedulerLogMaxBytes, 10)
	}
	return env
}

// buildScheduledInvocationScript builds and returns a derived value.
func buildScheduledInvocationScript(prompt, cwd string) (string, error) {
	return buildScheduledInvocationScriptWithEnv(prompt, cwd, schedulerInvocationEnv())
}

// buildScheduledInvocationScriptWithEnv builds and returns a derived value.
func buildScheduledInvocationScriptWithEnv(prompt, cwd string, env map[string]string) (string, error) {
	trimmedPrompt := strings.TrimSpace(prompt)
	if trimmedPrompt == "" {
		return "", errors.New("prompt must be a non-empty string")
	}
	if strings.TrimSpace(cwd) == "" {
		current, err := osGetwd()
		if err != nil {
			return "", err
		}
		cwd = current
	}
	ashPath, err := osExecutable()
	if err != nil {
		return "", err
	}

	parts := []string{fmt.Sprintf("cd %s", shellQuote(cwd))}
	envKeys := make([]string, 0, len(env))
	for key := range env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	assignments := make([]string, 0, len(envKeys))
	for _, key := range envKeys {
		assignments = append(assignments, fmt.Sprintf("%s=%s", key, shellQuote(env[key])))
	}
	command := fmt.Sprintf("%s %s", shellQuote(ashPath), shellQuote(trimmedPrompt))
	if len(assignments) > 0 {
		command = strings.Join(assignments, " ") + " " + command
	}
	parts = append(parts, command)
	return strings.Join(parts, " && "), nil
}

// shellQuote returns the computed value for this helper.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// schedulerLogFilePath returns the computed value for this helper.
func schedulerLogFilePath(isScheduledTask bool) (string, error) {
	sessionID, err := sanitizedSessionIDForLogFile()
	if err != nil {
		return "", err
	}
	return schedulerLogFilePathForSession(sessionID, isScheduledTask)
}

// schedulerLogFilePathForSession returns the computed value for this helper.
func schedulerLogFilePathForSession(sessionID string, isScheduledTask bool) (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	sanitized := sessionIDSanitizer.ReplaceAllString(strings.TrimSpace(sessionID), "")
	if sanitized == "" {
		return "", errors.New("SESSION_ID must be set for log file naming")
	}
	if isScheduledTask {
		sanitized = "task_" + sanitized
	}
	return filepath.Join(home, ashWorkspaceDirName, schedulerLogDirName, sanitized+".log"), nil
}

// sanitizedSessionIDForLogFile returns the computed value for this helper.
func sanitizedSessionIDForLogFile() (string, error) {
	raw := strings.TrimSpace(os.Getenv(sessionIDEnvName))
	if raw == "" {
		return "", errors.New("SESSION_ID is required for log file naming")
	}
	sanitized := sessionIDSanitizer.ReplaceAllString(raw, "")
	if sanitized == "" {
		return "", errors.New("SESSION_ID must contain at least one ASCII letter or digit")
	}
	return sanitized, nil
}

// ensureSessionID ensures required state exists and is up to date.
func ensureSessionID() (string, error) {
	if existing, err := sanitizedSessionIDForLogFile(); err == nil {
		return existing, nil
	}

	generated, err := generateSessionID()
	if err != nil {
		return "", err
	}
	if err := os.Setenv(sessionIDEnvName, generated); err != nil {
		return "", err
	}
	return generated, nil
}

// generateSessionID returns the computed value for this helper.
func generateSessionID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// buildFuturePromptLaunchAgent builds and returns a derived value.
func buildFuturePromptLaunchAgent(prompt, cwd string, scheduledAt time.Time) (string, string, string, error) {
	trimmedPrompt := strings.TrimSpace(prompt)
	if trimmedPrompt == "" {
		return "", "", "", errors.New("prompt must be a non-empty string")
	}
	if strings.TrimSpace(cwd) == "" {
		current, err := osGetwd()
		if err != nil {
			return "", "", "", err
		}
		cwd = current
	}
	ashPath, err := osExecutable()
	if err != nil {
		return "", "", "", err
	}
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", "", "", err
	}
	agentID := timeNow().UnixNano()
	label := fmt.Sprintf("%s.%d", futurePromptAgentPrefix, agentID)
	plistFile := fmt.Sprintf("%s.%d.plist", futurePromptAgentPrefix, agentID)
	plistPath := filepath.Join(root, launchAgentsDirName, plistFile)
	plist := buildLaunchAgentPlist(label, []string{ashPath, trimmedPrompt}, schedulerInvocationEnv(), cwd, scheduledAt)
	return label, plistPath, plist, nil
}

// buildLaunchAgentPlist builds and returns a derived value.
func buildLaunchAgentPlist(label string, programArgs []string, env map[string]string, cwd string, scheduledAt time.Time) string {
	envKeys := make([]string, 0, len(env))
	for key := range env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)

	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://apple.com\">\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")
	b.WriteString("    <key>Label</key>\n")
	b.WriteString(fmt.Sprintf("    <string>%s</string>\n", xmlEscape(label)))
	b.WriteString("    <key>ProgramArguments</key>\n")
	b.WriteString("    <array>\n")
	for _, arg := range programArgs {
		b.WriteString(fmt.Sprintf("        <string>%s</string>\n", xmlEscape(arg)))
	}
	b.WriteString("    </array>\n")
	b.WriteString("    <key>EnvironmentVariables</key>\n")
	b.WriteString("    <dict>\n")
	for _, key := range envKeys {
		b.WriteString(fmt.Sprintf("        <key>%s</key>\n", xmlEscape(key)))
		b.WriteString(fmt.Sprintf("        <string>%s</string>\n", xmlEscape(env[key])))
	}
	b.WriteString("    </dict>\n")
	b.WriteString("    <key>WorkingDirectory</key>\n")
	b.WriteString(fmt.Sprintf("    <string>%s</string>\n", xmlEscape(cwd)))
	b.WriteString("    <key>RunAtLoad</key>\n")
	b.WriteString("    <false/>\n")
	b.WriteString("    <key>StartCalendarInterval</key>\n")
	b.WriteString("    <dict>\n")
	b.WriteString(fmt.Sprintf("        <key>Year</key>\n        <integer>%d</integer>\n", scheduledAt.Year()))
	b.WriteString(fmt.Sprintf("        <key>Month</key>\n        <integer>%d</integer>\n", int(scheduledAt.Month())))
	b.WriteString(fmt.Sprintf("        <key>Day</key>\n        <integer>%d</integer>\n", scheduledAt.Day()))
	b.WriteString(fmt.Sprintf("        <key>Hour</key>\n        <integer>%d</integer>\n", scheduledAt.Hour()))
	b.WriteString(fmt.Sprintf("        <key>Minute</key>\n        <integer>%d</integer>\n", scheduledAt.Minute()))
	b.WriteString("    </dict>\n")
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

// parseFutureScheduleTime parses and validates input values.
func parseFutureScheduleTime(value string, now time.Time) (time.Time, error) {
	trimmed := normalizeFutureScheduleTime(value)
	if trimmed == "" {
		return time.Time{}, errors.New("when must be a non-empty string")
	}

	lower := strings.ToLower(trimmed)
	nowPlusPattern := regexp.MustCompile(`^now\s*\+\s*(\d+)\s+(second|seconds|minute|minutes|hour|hours|day|days|week|weeks)$`)
	if matches := nowPlusPattern.FindStringSubmatch(lower); len(matches) == 3 {
		amount, _ := strconv.Atoi(matches[1])
		scheduled, err := addScheduleOffset(now, amount, matches[2])
		if err != nil {
			return time.Time{}, err
		}
		if !scheduled.After(now) {
			return time.Time{}, errors.New("when must resolve to a future time")
		}
		return scheduled, nil
	}

	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		if !parsed.After(now) {
			return time.Time{}, errors.New("when must resolve to a future time")
		}
		return parsed.In(now.Location()), nil
	}

	formats := []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04"}
	for _, format := range formats {
		if parsed, err := time.ParseInLocation(format, trimmed, now.Location()); err == nil {
			if !parsed.After(now) {
				return time.Time{}, errors.New("when must resolve to a future time")
			}
			return parsed, nil
		}
	}

	return time.Time{}, errors.New("unsupported when format; use 'now + 5 minutes', 'in 10 minutes', or an RFC3339 timestamp")
}

// addScheduleOffset returns the computed value for this helper.
func addScheduleOffset(now time.Time, amount int, unit string) (time.Time, error) {
	if amount <= 0 {
		return time.Time{}, errors.New("time offset must be greater than zero")
	}

	switch unit {
	case "second", "seconds":
		return now.Add(time.Duration(amount) * time.Second), nil
	case "minute", "minutes":
		return now.Add(time.Duration(amount) * time.Minute), nil
	case "hour", "hours":
		return now.Add(time.Duration(amount) * time.Hour), nil
	case "day", "days":
		return now.AddDate(0, 0, amount), nil
	case "week", "weeks":
		return now.AddDate(0, 0, amount*7), nil
	default:
		return time.Time{}, errors.New("unsupported time unit")
	}
}

// xmlEscape returns the computed value for this helper.
func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

// normalizeFutureScheduleTime normalizes and returns the canonical value.
func normalizeFutureScheduleTime(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return trimmed
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "now + ") {
		return trimmed
	}

	fromNowPattern := regexp.MustCompile(`^(\d+)\s+(second|seconds|minute|minutes|hour|hours|day|days|week|weeks)\s+from\s+now$`)
	if matches := fromNowPattern.FindStringSubmatch(lower); len(matches) == 3 {
		return fmt.Sprintf("now + %s %s", matches[1], matches[2])
	}

	inPattern := regexp.MustCompile(`^in\s+(\d+)\s+(second|seconds|minute|minutes|hour|hours|day|days|week|weeks)$`)
	if matches := inPattern.FindStringSubmatch(lower); len(matches) == 3 {
		return fmt.Sprintf("now + %s %s", matches[1], matches[2])
	}

	return trimmed
}

// optionalStringArg returns the computed value for this helper.
func optionalStringArg(args map[string]any, key string) (string, error) {
	if _, ok := args[key]; !ok {
		return "", nil
	}
	raw, ok := args[key].(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(raw), nil
}

// validateCronExpr returns the computed value for this helper.
func validateCronExpr(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return errors.New("cron must be a non-empty string")
	}
	if strings.HasPrefix(expr, "@") {
		allowed := map[string]struct{}{"@yearly": {}, "@annually": {}, "@monthly": {}, "@weekly": {}, "@daily": {}, "@hourly": {}}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(expr))]; ok {
			return nil
		}
		return errors.New("unsupported cron macro")
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return errors.New("cron must contain 5 fields")
	}
	return nil
}

// buildRecurringJobLine builds and returns a derived value.
func buildRecurringJobLine(prompt, cronExpr, cwd, purpose, id string) (recurringJobMetadata, string, error) {
	env := schedulerEnvAllowlist()
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("job-%d", timeNow().UnixNano())
	}
	meta := recurringJobMetadata{
		ID:        strings.TrimSpace(id),
		Cron:      strings.TrimSpace(cronExpr),
		Prompt:    strings.TrimSpace(prompt),
		Cwd:       strings.TrimSpace(cwd),
		Env:       env,
		Purpose:   strings.TrimSpace(purpose),
		CreatedAt: timeNow().Format(time.RFC3339),
	}
	script, err := buildScheduledInvocationScriptWithEnv(meta.Prompt, meta.Cwd, meta.Env)
	if err != nil {
		return recurringJobMetadata{}, "", err
	}
	line, err := buildRecurringCrontabLine(meta, script)
	if err != nil {
		return recurringJobMetadata{}, "", err
	}
	return meta, line, nil
}

// buildRecurringCrontabLine builds and returns a derived value.
func buildRecurringCrontabLine(meta recurringJobMetadata, script string) (string, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	encoded := base64.RawStdEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s %s %s%s %s", meta.Cron, script, jobMarkerPrefix, meta.ID, encoded), nil
}

// appendCrontabLine appends content and returns the updated result.
func appendCrontabLine(content, line string) string {
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return line + "\n"
	}
	return trimmed + "\n" + line + "\n"
}

// parseRecurringJobs parses and validates input values.
func parseRecurringJobs(content string) ([]recurringJobRecord, error) {
	lines := strings.Split(content, "\n")
	records := make([]recurringJobRecord, 0)
	for _, line := range lines {
		idx := strings.Index(line, jobMarkerPrefix)
		if idx < 0 {
			continue
		}
		suffix := strings.TrimSpace(line[idx+len(jobMarkerPrefix):])
		parts := strings.Fields(suffix)
		if len(parts) < 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		encoded := strings.TrimSpace(parts[1])
		decoded, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, err
		}
		var meta recurringJobMetadata
		if err := json.Unmarshal(decoded, &meta); err != nil {
			return nil, err
		}
		if meta.ID == "" {
			meta.ID = id
		}
		commandText := strings.TrimSpace(line)
		if idx > 0 {
			commandText = strings.TrimSpace(line[:idx])
		}
		records = append(records, recurringJobRecord{Meta: meta, Line: line, Command: commandText})
	}
	return records, nil
}

// findRecurringJob returns the computed value for this helper.
func findRecurringJob(records []recurringJobRecord, id string) (recurringJobRecord, bool) {
	for _, rec := range records {
		if rec.Meta.ID == id {
			return rec, true
		}
	}
	return recurringJobRecord{}, false
}

// removeRecurringJobFromCrontab returns the computed value for this helper.
func removeRecurringJobFromCrontab(content, id string) (string, bool) {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	needle := jobMarkerPrefix + id + " "
	for _, line := range lines {
		if strings.Contains(line, needle) {
			removed = true
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return "", removed
	}
	return strings.Join(kept, "\n") + "\n", removed
}

// replaceRecurringJobLine replaces content and returns the updated result.
func replaceRecurringJobLine(content, id, replacement string) string {
	lines := strings.Split(content, "\n")
	updated := make([]string, 0, len(lines))
	needle := jobMarkerPrefix + id + " "
	for _, line := range lines {
		if strings.Contains(line, needle) {
			updated = append(updated, replacement)
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		updated = append(updated, line)
	}
	if len(updated) == 0 {
		return ""
	}
	return strings.Join(updated, "\n") + "\n"
}

// loadCurrentCrontab loads data from storage.
func loadCurrentCrontab(ctx context.Context) (string, error) {
	result := runToolCommandWithInput(ctx, "crontab", []string{"-l"}, "", defaultCronTimeout, toolOutputLimit())
	if result.OK {
		return result.Stdout, nil
	}
	combined := strings.ToLower(strings.TrimSpace(result.Stderr + " " + result.Error))
	if strings.Contains(combined, "no crontab") {
		return "", nil
	}
	return "", errors.New(strings.TrimSpace(result.Stderr + " " + result.Error))
}

// writeCrontab writes data to the target destination.
func writeCrontab(ctx context.Context, content string) toolCommandResult {
	return runToolCommandWithInput(ctx, "crontab", []string{"-"}, content, defaultCronTimeout, toolOutputLimit())
}

// runToolCommandWithInput runs the requested operation.
func runToolCommandWithInput(ctx context.Context, name string, args []string, stdin string, timeout time.Duration, outputMax int) toolCommandResult {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := execCommandContext(commandCtx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := toolCommandResult{
		OK:      err == nil,
		Command: strings.TrimSpace(strings.Join(append([]string{name}, args...), " ")),
		Stdout:  truncateForToolOutput(stdout.String(), outputMax),
		Stderr:  truncateForToolOutput(stderr.String(), outputMax),
	}
	if err == nil {
		result.ExitCode = 0
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		result.Error = fmt.Sprintf("command exited with status %d", result.ExitCode)
		return result
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		result.Error = fmt.Sprintf("command timed out after %s", timeout)
		return result
	}
	result.ExitCode = -1
	result.Error = err.Error()
	return result
}

// ashWorkspaceDir returns the computed value for this helper.
func ashWorkspaceDir() (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ashWorkspaceDirName), nil
}

// resolveWorkspacePath returns the computed value for this helper.
func resolveWorkspacePath(root, userPath string) (absolute string, rel string, err error) {
	cleanInput := strings.TrimSpace(userPath)
	if cleanInput == "" {
		return "", "", errors.New("path must be a non-empty string")
	}

	if filepath.IsAbs(cleanInput) {
		relPath, relErr := filepath.Rel(root, cleanInput)
		if relErr != nil {
			return "", "", relErr
		}
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", "", errors.New("path must be inside ~/.ash")
		}
		return cleanInput, filepath.ToSlash(relPath), nil
	}

	joined := filepath.Join(root, cleanInput)
	clean := filepath.Clean(joined)
	relPath, relErr := filepath.Rel(root, clean)
	if relErr != nil {
		return "", "", relErr
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path must be inside ~/.ash")
	}
	return clean, filepath.ToSlash(relPath), nil
}

// updateWorkspaceInventory returns the computed value for this helper.
func updateWorkspaceInventory(root, relPath, purpose string) error {
	if filepath.ToSlash(relPath) == inventoryFileName {
		return nil
	}

	inventoryPath := filepath.Join(root, inventoryFileName)
	entries := map[string]string{}
	if content, err := osReadFile(inventoryPath); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "|", 2)
			if len(parts) != 2 {
				continue
			}
			entries[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	entries[filepath.ToSlash(relPath)] = strings.TrimSpace(purpose)
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(" | ")
		b.WriteString(entries[key])
		b.WriteString("\n")
	}
	return osWriteFile(inventoryPath, []byte(b.String()), 0o600)
}

// runToolCommand runs the requested operation.
func runToolCommand(ctx context.Context, name string, args []string, timeout time.Duration, outputMax int) toolCommandResult {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := execCommandContext(commandCtx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := toolCommandResult{
		OK:      err == nil,
		Command: strings.TrimSpace(strings.Join(append([]string{name}, args...), " ")),
		Stdout:  truncateForToolOutput(stdout.String(), outputMax),
		Stderr:  truncateForToolOutput(stderr.String(), outputMax),
	}

	if err == nil {
		result.ExitCode = 0
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		result.Error = fmt.Sprintf("command exited with status %d", result.ExitCode)
		return result
	}

	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		result.Error = fmt.Sprintf("command timed out after %s", timeout)
		return result
	}

	result.ExitCode = -1
	result.Error = err.Error()
	return result
}

// truncateForToolOutput truncates output to the configured maximum length.
func truncateForToolOutput(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "\n...truncated..."
}

// toStringArg converts the value to a string argument when possible.
func toStringArg(value any) (string, bool) {
	v, ok := value.(string)
	if !ok {
		return "", false
	}
	return v, true
}

// toStringSliceArg converts the value to a string argument when possible.
func toStringSliceArg(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}

	raw, ok := value.([]any)
	if !ok {
		return nil, errors.New("args must be an array of strings")
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		v, ok := item.(string)
		if !ok {
			return nil, errors.New("args must be an array of strings")
		}
		if v == "" {
			continue
		}
		out = append(out, v)
	}

	return out, nil
}

// isBlockedArgument reports whether the condition is true.
func isBlockedArgument(arg string) bool {
	return argumentBlockPattern.MatchString(arg)
}

// sanitizeJSONError returns the computed value for this helper.
func sanitizeJSONError(value string) string {
	value = strings.ReplaceAll(value, `"`, `'`)
	return strings.ReplaceAll(value, "\n", " ")
}

// startThinkingIndicator starts the thinking indicator and returns a stop function.
func startThinkingIndicator(w io.Writer) func() {
	frames := []string{"|", "/", "-", "\\"}
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		frame := 0
		for {
			fmt.Fprintf(w, "\rThinking... %s", frames[frame])
			frame = (frame + 1) % len(frames)

			select {
			case <-done:
				fmt.Fprint(w, "\r                \r")
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}

// renderMarkdownWithGlamour renders markdown using terminal styling.
func renderMarkdownWithGlamour(markdown string) (string, error) {
	renderer, err := newTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return "", err
	}

	return renderer.Render(markdown)
}

// formatAssistantOutput formats assistant output for display.
func formatAssistantOutput(raw string) string {
	rendered, err := markdownRenderer(raw)
	if err != nil {
		return ensureSingleTrailingNewline(raw)
	}

	return ensureSingleTrailingNewline(rendered)
}

// ensureSingleTrailingNewline ensures required state exists and is up to date.
func ensureSingleTrailingNewline(value string) string {
	trimmed := strings.TrimRight(value, "\n")
	return trimmed + "\n"
}
