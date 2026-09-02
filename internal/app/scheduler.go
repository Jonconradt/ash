package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// schedulerEnvAllowlist returns the subset of environment variables that should be inherited by scheduled ash invocations.
func schedulerEnvAllowlist() map[string]string {
	keys := []string{
		aiEnvEndpoint,
		aiEnvModel,
		aiEnvAuthType,
		aiEnvAuthToken,
		aiEnvProvider,
		aiEnvCache,
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
		maxAgentsEnvName,
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

// schedulerInvocationEnv returns the environment used when a scheduled ash task launches a new process.
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
			_, _ = fmt.Fprintln(os.Stderr, err)
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

// buildScheduledInvocationScript constructs a shell command that re-invokes ash for a scheduled prompt in the requested working directory.
func buildScheduledInvocationScript(prompt, cwd string) (string, error) {
	return buildScheduledInvocationScriptWithEnv(prompt, cwd, schedulerInvocationEnv())
}

// buildScheduledInvocationScriptWithEnv constructs a shell command that re-invokes ash for a scheduled prompt with the supplied environment.
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

// shellQuote escapes a string so it can be safely embedded in a shell command.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// schedulerLogFilePath returns the log file path for the current ash session, using the scheduled-task suffix when applicable.
func schedulerLogFilePath(isScheduledTask bool) (string, error) {
	sessionID, err := sanitizedSessionIDForLogFile()
	if err != nil {
		return "", err
	}
	return schedulerLogFilePathForSession(sessionID, isScheduledTask)
}

// schedulerLogFilePathForSession returns the log file path for the supplied session ID and task type.
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

// sanitizedSessionIDForLogFile returns the sanitized session ID used for history and log file naming.
func sanitizedSessionIDForLogFile() (string, error) {
	raw := strings.TrimSpace(os.Getenv(sessionIDEnvName))
	if raw == "" {
		return "", errors.New("SESSION_ID is required for log file naming")
	}
	if sessionIDPattern.MatchString(raw) {
		if !validSessionID(raw) {
			return "", errors.New("SESSION_ID is too long")
		}
		return raw, nil
	}
	sanitized := sessionIDSanitizer.ReplaceAllString(raw, "")
	if sanitized == "" {
		return "", errors.New("SESSION_ID must contain at least one ASCII letter or digit")
	}
	if len(sanitized) > maxSessionIDLength {
		return "", errors.New("SESSION_ID is too long")
	}
	return sanitized, nil
}

// ensureSessionID ensures a session ID exists in the environment and returns it.
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

// generateSessionID creates a random session identifier for the current ash run.
func generateSessionID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// buildFuturePromptLaunchAgent creates a launch agent plist that will run ash later with the supplied prompt and working directory.
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

// buildLaunchAgentPlist renders the launchd plist content for a scheduled ash invocation.
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
	fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(label))
	b.WriteString("    <key>ProgramArguments</key>\n")
	b.WriteString("    <array>\n")
	for _, arg := range programArgs {
		fmt.Fprintf(&b, "        <string>%s</string>\n", xmlEscape(arg))
	}
	b.WriteString("    </array>\n")
	b.WriteString("    <key>EnvironmentVariables</key>\n")
	b.WriteString("    <dict>\n")
	for _, key := range envKeys {
		fmt.Fprintf(&b, "        <key>%s</key>\n", xmlEscape(key))
		fmt.Fprintf(&b, "        <string>%s</string>\n", xmlEscape(env[key]))
	}
	b.WriteString("    </dict>\n")
	b.WriteString("    <key>WorkingDirectory</key>\n")
	fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(cwd))
	b.WriteString("    <key>RunAtLoad</key>\n")
	b.WriteString("    <false/>\n")
	b.WriteString("    <key>StartCalendarInterval</key>\n")
	b.WriteString("    <dict>\n")
	fmt.Fprintf(&b, "        <key>Year</key>\n        <integer>%d</integer>\n", scheduledAt.Year())
	fmt.Fprintf(&b, "        <key>Month</key>\n        <integer>%d</integer>\n", int(scheduledAt.Month()))
	fmt.Fprintf(&b, "        <key>Day</key>\n        <integer>%d</integer>\n", scheduledAt.Day())
	fmt.Fprintf(&b, "        <key>Hour</key>\n        <integer>%d</integer>\n", scheduledAt.Hour())
	fmt.Fprintf(&b, "        <key>Minute</key>\n        <integer>%d</integer>\n", scheduledAt.Minute())
	b.WriteString("    </dict>\n")
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

// parseFutureScheduleTime parses a natural-language schedule expression and resolves it to a future timestamp.
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

// addScheduleOffset adds a relative time offset to now using the supplied unit.
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

// xmlEscape escapes XML-sensitive characters in a string for plist content.
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

// normalizeFutureScheduleTime converts common relative scheduling phrases into the canonical form expected by the parser.
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

// optionalStringArg returns the string value for key when present and valid, or an empty string when absent.
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

// validateCronExpr ensures a cron expression is either a supported macro or a structurally valid five-field schedule.
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

// buildRecurringJobLine creates recurring-job metadata and the corresponding crontab line for a scheduled ash invocation.
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

// buildRecurringCrontabLine renders a single crontab entry that embeds recurring-job metadata and the invocation script.
func buildRecurringCrontabLine(meta recurringJobMetadata, script string) (string, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	encoded := base64.RawStdEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s %s %s%s %s", meta.Cron, script, jobMarkerPrefix, meta.ID, encoded), nil
}

// appendCrontabLine appends a crontab entry to existing content, preserving the trailing newline behavior.
func appendCrontabLine(content, line string) string {
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return line + "\n"
	}
	return trimmed + "\n" + line + "\n"
}

// parseRecurringJobs parses recurring-job entries from crontab content and decodes their metadata payloads.
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

// findRecurringJob returns the recurring-job record whose metadata ID matches the supplied value.
func findRecurringJob(records []recurringJobRecord, id string) (recurringJobRecord, bool) {
	for _, rec := range records {
		if rec.Meta.ID == id {
			return rec, true
		}
	}
	return recurringJobRecord{}, false
}

// removeRecurringJobFromCrontab removes the recurring job with the supplied ID from crontab content.
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

// replaceRecurringJobLine replaces the crontab line for the recurring job with the supplied ID.
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

// loadCurrentCrontab reads the current user crontab contents, if any.
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

// writeCrontab writes the supplied crontab content to the user's cron table.
func writeCrontab(ctx context.Context, content string) toolCommandResult {
	return runToolCommandWithInput(ctx, "crontab", []string{"-"}, content, defaultCronTimeout, toolOutputLimit())
}
