package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type mcpToolShim interface {
	ListTools() []toolDefinition
	CallTool(ctx context.Context, name string, args map[string]any) string
}

type localToolShim struct {
	allowlist map[string]struct{}
	agents    *agentBudget
}

// ListTools returns the tool definitions exposed to the AI client by the local shim.
func (s localToolShim) ListTools() []toolDefinition {
	runUnixDescription := "Run a single allowlisted Unix executable with direct args and no shell expansion. For pipelines such as copying ls output to the clipboard, use run_unix_pipeline"
	allowed := sortedAllowlist(s.allowlist)
	if len(allowed) > 0 {
		runUnixDescription += ". Allowlisted executables: " + strings.Join(allowed, ", ")
	}

	tools := []toolDefinition{
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "run_unix_command",
				Description: runUnixDescription,
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "Executable name to run (must be allowlisted)",
						},
						"args": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "string",
							},
							"description": "Direct argv passed to the executable",
						},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "run_unix_pipeline",
				Description: "Run a pipeline of 2 to 16 allowlisted Unix executables without a shell; use this for operations such as ls | grep pattern | pbcopy",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pipeline": map[string]any{
							"type":        "string",
							"description": "Two to sixteen allowlisted commands separated by |, for example ls | grep pattern | pbcopy",
						},
					},
					"required": []string{"pipeline"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "schedule_future_prompt",
				Description: "Schedule one future ash invocation with a prompt using a user launchd LaunchAgent; accepts common offsets like '2 minutes from now'",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":        "string",
							"description": "Prompt text to run later",
						},
						"when": map[string]any{
							"type":        "string",
							"description": "Future schedule string such as 'now + 5 minutes', 'in 10 minutes', or RFC3339 datetime",
						},
						"cwd": map[string]any{
							"type":        "string",
							"description": "Optional working directory for the scheduled invocation",
						},
					},
					"required": []string{"prompt", "when"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "schedule_recurring_prompt",
				Description: "Schedule a recurring ash invocation with a cron expression",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":        "string",
							"description": "Prompt text to run on schedule",
						},
						"cron": map[string]any{
							"type":        "string",
							"description": "Cron expression (5 fields) or @weekly/@daily style macro",
						},
						"cwd": map[string]any{
							"type":        "string",
							"description": "Optional working directory for the scheduled invocation",
						},
						"purpose": map[string]any{
							"type":        "string",
							"description": "Optional purpose text for job explain output",
						},
					},
					"required": []string{"prompt", "cron"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "manage_recurring_jobs",
				Description: "List, cancel, modify, or explain recurring ash cron jobs",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"description": "One of list|cancel|modify|explain",
						},
						"id": map[string]any{
							"type":        "string",
							"description": "Recurring job id (required for cancel/modify/explain single job)",
						},
						"cron": map[string]any{
							"type":        "string",
							"description": "Replacement cron expression for modify",
						},
						"prompt": map[string]any{
							"type":        "string",
							"description": "Replacement prompt text for modify",
						},
						"cwd": map[string]any{
							"type":        "string",
							"description": "Replacement working directory for modify",
						},
						"purpose": map[string]any{
							"type":        "string",
							"description": "Replacement purpose text for modify",
						},
					},
					"required": []string{"action"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "ash_read_workspace_file",
				Description: "Read a file inside ~/.ash workspace",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path relative to ~/.ash",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "ash_write_workspace_file",
				Description: "Write a file inside ~/.ash workspace and update inventory.md",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path relative to ~/.ash",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "File contents to write",
						},
						"purpose": map[string]any{
							"type":        "string",
							"description": "Purpose text stored in ~/.ash/inventory.md",
						},
					},
					"required": []string{"path", "content", "purpose"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "ash_read_scratch_file",
				Description: "Read a file in the current session's managed scratch directory under ~/.ash/scratch",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path relative to the current session scratch root",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "ash_write_scratch_file",
				Description: "Write a file in the current session's managed scratch directory under ~/.ash/scratch",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path relative to the current session scratch root",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "File contents to write",
						},
						"purpose": map[string]any{
							"type":        "string",
							"description": "Optional purpose text for scratch tracking",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "ash_append_scratch_file",
				Description: "Append content to a file in the current session's managed scratch directory under ~/.ash/scratch",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path relative to the current session scratch root",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Content to append",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "ash_edit_scratch_file",
				Description: "Replace a file in the current session's managed scratch directory under ~/.ash/scratch",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Path relative to the current session scratch root",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Replacement file contents",
						},
					},
					"required": []string{"path", "content"},
				},
			},
		},
	}
	if !isChildAgent() {
		tools = append(tools, toolDefinition{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "run_sub_agent",
				Description: "Delegate one focused, independent task to a child ash agent. The child has the same working directory, configuration, tools, and OS permissions, returns a bounded result, and cannot delegate again. Use only when the task is worth the delegation overhead; do not include the parent conversation or secrets in the prompt.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"prompt": map[string]any{
							"type":        "string",
							"minLength":   1,
							"maxLength":   maxAgentPromptBytes,
							"description": "Concise objective with only essential constraints, relevant paths, and expected compact result.",
						},
					},
					"required": []string{"prompt"},
				},
			},
		})
	}
	return tools
}

// CallTool dispatches a tool call to the matching local shim handler and returns the serialized result.
func (s localToolShim) CallTool(ctx context.Context, name string, args map[string]any) string {
	var result toolCommandResult

	switch name {
	case "run_sub_agent":
		result = s.callSubAgent(ctx, args)
	case "run_unix_command":
		result = s.callUnixCommand(ctx, args)
	case "run_unix_pipeline":
		result = s.callUnixPipeline(ctx, args)
	case "run_python3", "python3":
		result = s.callPython3(ctx, args)
	case "schedule_future_prompt":
		result = s.callScheduleFuturePrompt(ctx, args)
	case "schedule_recurring_prompt":
		result = s.callScheduleRecurringPrompt(ctx, args)
	case "manage_recurring_jobs":
		result = s.callManageRecurringJobs(ctx, args)
	case "ash_read_workspace_file":
		result = s.callReadWorkspaceFile(args)
	case "ash_write_workspace_file":
		result = s.callWriteWorkspaceFile(args)
	case "ash_read_scratch_file":
		result = s.callReadScratchFile(args)
	case "ash_write_scratch_file":
		result = s.callWriteScratchFile(args)
	case "ash_append_scratch_file":
		result = s.callAppendScratchFile(args)
	case "ash_edit_scratch_file":
		result = s.callEditScratchFile(args)
	default:
		result = toolCommandResult{OK: false, Error: fmt.Sprintf("unknown tool: %s", name), EID: "Ryr9hU7l"}
	}
	result.Untrusted = true

	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":"failed to encode tool result: %s"}`, sanitizeJSONError(err.Error()))
	}

	return string(encoded)
}

func (s localToolShim) callSubAgent(ctx context.Context, args map[string]any) toolCommandResult {
	if isChildAgent() {
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: "child agents cannot create sub-agents", EID: "J9QJ8y8p"}
	}
	prompt, ok := toStringArg(args["prompt"])
	if !ok || strings.TrimSpace(prompt) == "" {
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: "prompt must be a non-empty string", EID: "J9QJ8y8p"}
	}
	if !utf8.ValidString(prompt) {
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: "prompt must be valid UTF-8", EID: "J9QJ8y8p"}
	}
	if len(prompt) > maxAgentPromptBytes {
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: fmt.Sprintf("prompt exceeds %d bytes", maxAgentPromptBytes), EID: "J9QJ8y8p"}
	}
	for key := range args {
		if key != "prompt" {
			return toolCommandResult{OK: false, Command: "run_sub_agent", Error: "unsupported sub-agent argument", EID: "J9QJ8y8p"}
		}
	}
	if s.agents == nil || !s.agents.reserve() {
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: "maximum sub-agent count reached", EID: "J9QJ8y8p"}
	}

	parentID, err := sanitizedSessionIDForLogFile()
	if err != nil {
		s.agents.release()
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: err.Error(), EID: "J9QJ8y8p"}
	}
	childID, err := generateChildSessionID(parentID)
	if err != nil {
		s.agents.release()
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: err.Error(), EID: "J9QJ8y8p"}
	}
	started := time.Now()
	result := runSubAgentCommand(ctx, prompt, childID)
	slog.Debug("sub-agent completed", "request_id", requestIDGenerator(), "parent_session_id", parentID, "child_session_id", childID, "ok", result.OK, "exit_code", result.ExitCode, "stdout_bytes", len(result.Stdout), "stderr_bytes", len(result.Stderr), "EID", "QeR8y5aL")
	if metrics := executionMetricsFromContext(ctx); metrics != nil {
		metrics.addSubAgent(time.Since(started), result)
	}
	return result
}

// callUnixPipeline executes a validated pipeline without invoking a shell.
func (s localToolShim) callUnixPipeline(ctx context.Context, args map[string]any) toolCommandResult {
	if isChildAgent() && pipelineContainsAsh(args) {
		return toolCommandResult{OK: false, Command: "run_unix_pipeline", Error: "child agents cannot invoke ash", EID: "J9QJ8y8p"}
	}
	pipeline, ok := toStringArg(args["pipeline"])
	if !ok {
		return toolCommandResult{OK: false, Error: "pipeline must be a string", EID: "jGDQaWr5"}
	}

	parts := strings.Split(pipeline, "|")
	if len(parts) < 2 || len(parts) > 16 {
		return toolCommandResult{OK: false, Command: pipeline, Error: "pipeline must contain between 2 and 16 commands separated by |", EID: "8Q8QmB9t"}
	}

	commands := make([][]string, 0, len(parts))
	for _, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			return toolCommandResult{OK: false, Command: pipeline, Error: "pipeline commands must not be empty", EID: "o3UdEAP7"}
		}
		commandName := normalizeToolName(fields[0])
		if commandName == "" {
			return toolCommandResult{OK: false, Command: pipeline, Error: "pipeline command must be a bare executable name", EID: "o3UdEAP7"}
		}
		if _, allowed := s.allowlist[commandName]; !allowed {
			return toolCommandResult{OK: false, Command: commandName, Error: "command is not allowlisted", EID: "ABLaPipP"}
		}
		for _, arg := range fields[1:] {
			if isBlockedArgument(arg) {
				return toolCommandResult{OK: false, Command: pipeline, Error: "argument contains blocked shell control pattern", EID: "nnbIek1C"}
			}
		}
		commands = append(commands, fields)
	}

	return toolPipelineRunner(ctx, commands, pipeline, toolTimeout(), toolOutputLimit())
}

// callUnixCommand executes an allowlisted Unix command with the provided arguments and validates safety constraints.
func (s localToolShim) callUnixCommand(ctx context.Context, args map[string]any) toolCommandResult {
	commandInput, ok := toStringArg(args["command"])
	if !ok {
		return toolCommandResult{OK: false, Error: "command must be a string", EID: "jGDQaWr5"}
	}

	fields := strings.Fields(commandInput)
	if len(fields) == 0 {
		return toolCommandResult{OK: false, Error: "command must be a bare executable name", EID: "8Q8QmB9t"}
	}

	commandName := fields[0]
	inlineArgs := fields[1:]

	commandName = normalizeToolName(commandName)
	if commandName == "" {
		return toolCommandResult{OK: false, Error: "command must be a bare executable name", EID: "o3UdEAP7"}
	}
	if isChildAgent() && isAshExecutableName(commandName) {
		return toolCommandResult{OK: false, Command: commandName, Error: "child agents cannot invoke ash", EID: "J9QJ8y8p"}
	}

	if _, allowed := s.allowlist[commandName]; !allowed {
		return toolCommandResult{OK: false, Command: commandName, Error: "command is not allowlisted", EID: "ABLaPipP"}
	}

	argv, err := toStringSliceArg(args["args"])
	if err != nil {
		return toolCommandResult{OK: false, Command: commandName, Error: err.Error(), EID: "8t4jO24H"}
	}
	argv = append(inlineArgs, argv...)

	for _, arg := range argv {
		if isBlockedArgument(arg) {
			return toolCommandResult{OK: false, Command: commandName, Error: "argument contains blocked shell control pattern", EID: "nnbIek1C"}
		}
	}

	return toolCommandRunner(ctx, commandName, argv, toolTimeout(), toolOutputLimit())
}

// callPython3 executes a Python snippet via python3 -c after validating the provided code and argv values.
func (s localToolShim) callPython3(ctx context.Context, args map[string]any) toolCommandResult {
	code, ok := toStringArg(args["code"])
	if !ok || strings.TrimSpace(code) == "" {
		return toolCommandResult{OK: false, Command: "python3", Error: "code must be a non-empty string", EID: "GmnCP0Ho"}
	}

	argv, err := toStringSliceArg(args["argv"])
	if err != nil {
		return toolCommandResult{OK: false, Command: "python3", Error: err.Error(), EID: "c6BJjKpr"}
	}

	for _, arg := range argv {
		if isBlockedArgument(arg) {
			return toolCommandResult{OK: false, Command: "python3", Error: "argv contains blocked shell control pattern", EID: "WT86KNdu"}
		}
	}

	pythonArgs := append([]string{"-c", code}, argv...)
	return toolCommandRunner(ctx, "python3", pythonArgs, toolTimeout(), toolOutputLimit())
}

// callScheduleFuturePrompt schedules a future ash prompt using launchd and returns the resulting execution status.
func (s localToolShim) callScheduleFuturePrompt(ctx context.Context, args map[string]any) toolCommandResult {
	if isChildAgent() {
		return toolCommandResult{OK: false, Command: "launchctl", Error: "child agents cannot schedule ash invocations", EID: "J9QJ8y8p"}
	}
	prompt, ok := toStringArg(args["prompt"])
	if !ok || strings.TrimSpace(prompt) == "" {
		return toolCommandResult{OK: false, Command: "launchctl", Error: "prompt must be a non-empty string", EID: "PLVMuQid"}
	}
	when, ok := toStringArg(args["when"])
	if !ok || strings.TrimSpace(when) == "" {
		return toolCommandResult{OK: false, Command: "launchctl", Error: "when must be a non-empty string", EID: "qMF65jqp"}
	}
	scheduledAt, err := parseFutureScheduleTime(when, timeNow())
	if err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error(), EID: "BYsHkqp5"}
	}

	cwd, err := optionalStringArg(args, "cwd")
	if err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error(), EID: "3qd8ULhl"}
	}

	label, plistPath, plistContent, err := buildFuturePromptLaunchAgent(prompt, cwd, scheduledAt)
	if err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error(), EID: "ldXnrVSd"}
	}
	if err := osMkdirAll(filepath.Dir(plistPath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error(), EID: "h8wUphkW"}
	}
	if err := osWriteFile(plistPath, []byte(plistContent), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error(), EID: "IN04IUqF"}
	}

	serviceDomain := fmt.Sprintf("gui/%d", os.Getuid())
	result := toolCommandRunner(ctx, "launchctl", []string{"bootstrap", serviceDomain, plistPath}, defaultLaunchdTimeout, toolOutputLimit())
	if !result.OK {
		return result
	}

	result.Stdout = fmt.Sprintf("scheduled future job label=%s at=%s plist=%s", label, scheduledAt.Format(time.RFC3339), plistPath)
	return result
}

// callScheduleRecurringPrompt creates a recurring crontab entry that re-invokes ash on the supplied schedule.
func (s localToolShim) callScheduleRecurringPrompt(ctx context.Context, args map[string]any) toolCommandResult {
	if isChildAgent() {
		return toolCommandResult{OK: false, Command: "crontab", Error: "child agents cannot schedule ash invocations", EID: "J9QJ8y8p"}
	}
	prompt, ok := toStringArg(args["prompt"])
	if !ok || strings.TrimSpace(prompt) == "" {
		return toolCommandResult{OK: false, Command: "crontab", Error: "prompt must be a non-empty string", EID: "isM7Rvej"}
	}

	cronExpr, ok := toStringArg(args["cron"])
	if !ok {
		return toolCommandResult{OK: false, Command: "crontab", Error: "cron must be a string", EID: "DFyb7D5R"}
	}
	cronExpr = strings.TrimSpace(cronExpr)
	if err := validateCronExpr(cronExpr); err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "KYq9yJfj"}
	}

	cwd, err := optionalStringArg(args, "cwd")
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "Sb3GpJXW"}
	}
	purpose, err := optionalStringArg(args, "purpose")
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "Aj2YDiWD"}
	}

	meta, line, err := buildRecurringJobLine(prompt, cronExpr, cwd, purpose, "")
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "x2NlkF1G"}
	}

	content, err := loadCurrentCrontab(ctx)
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab -l", Error: err.Error(), EID: "qCyMVGqt"}
	}
	updated := appendCrontabLine(content, line)
	writeResult := writeCrontab(ctx, updated)
	if !writeResult.OK {
		return writeResult
	}
	writeResult.Stdout = strings.TrimSpace(fmt.Sprintf("scheduled recurring job id=%s cron=%s", meta.ID, meta.Cron))
	return writeResult
}

// callManageRecurringJobs lists, explains, cancels, or modifies recurring ash jobs stored in the user's crontab.
func (s localToolShim) callManageRecurringJobs(ctx context.Context, args map[string]any) toolCommandResult {
	if isChildAgent() {
		return toolCommandResult{OK: false, Command: "crontab", Error: "child agents cannot manage ash invocations", EID: "J9QJ8y8p"}
	}
	actionRaw, ok := toStringArg(args["action"])
	if !ok {
		return toolCommandResult{OK: false, Command: "crontab", Error: "action must be a string", EID: "hFDmBJwy"}
	}
	action := strings.ToLower(strings.TrimSpace(actionRaw))

	content, err := loadCurrentCrontab(ctx)
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab -l", Error: err.Error(), EID: "KZSvjnlE"}
	}
	records, err := parseRecurringJobs(content)
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab -l", Error: err.Error(), EID: "kpCdXkJ4"}
	}

	switch action {
	case "list":
		body, _ := json.Marshal(records)
		return toolCommandResult{OK: true, Command: "crontab -l", ExitCode: 0, Stdout: string(body)}
	case "explain":
		id, _ := toStringArg(args["id"])
		id = strings.TrimSpace(id)
		if id == "" {
			var b strings.Builder
			if len(records) == 0 {
				b.WriteString("no recurring ash jobs found")
			} else {
				for _, rec := range records {
					fmt.Fprintf(&b, "id=%s cron=%s cwd=%s purpose=%s\n", rec.Meta.ID, rec.Meta.Cron, rec.Meta.Cwd, strings.TrimSpace(rec.Meta.Purpose))
				}
			}
			return toolCommandResult{OK: true, Command: "crontab -l", ExitCode: 0, Stdout: strings.TrimSpace(b.String())}
		}
		rec, found := findRecurringJob(records, id)
		if !found {
			return toolCommandResult{OK: false, Command: "crontab -l", Error: "recurring job id not found", EID: "vVYTo0QW"}
		}
		return toolCommandResult{
			OK:       true,
			Command:  "crontab -l",
			ExitCode: 0,
			Stdout:   fmt.Sprintf("id=%s cron=%s cwd=%s purpose=%s prompt=%s", rec.Meta.ID, rec.Meta.Cron, rec.Meta.Cwd, strings.TrimSpace(rec.Meta.Purpose), rec.Meta.Prompt),
		}
	case "cancel":
		id, _ := toStringArg(args["id"])
		id = strings.TrimSpace(id)
		if id == "" {
			return toolCommandResult{OK: false, Command: "crontab", Error: "id is required for cancel", EID: "Abfeqr4e"}
		}
		updated, removed := removeRecurringJobFromCrontab(content, id)
		if !removed {
			return toolCommandResult{OK: false, Command: "crontab", Error: "recurring job id not found", EID: "LXmp52fG"}
		}
		result := writeCrontab(ctx, updated)
		if !result.OK {
			return result
		}
		result.Stdout = fmt.Sprintf("canceled recurring job id=%s", id)
		return result
	case "modify":
		id, _ := toStringArg(args["id"])
		id = strings.TrimSpace(id)
		if id == "" {
			return toolCommandResult{OK: false, Command: "crontab", Error: "id is required for modify", EID: "odqZtaW5"}
		}
		rec, found := findRecurringJob(records, id)
		if !found {
			return toolCommandResult{OK: false, Command: "crontab", Error: "recurring job id not found", EID: "QwF0ZRUd"}
		}

		if cronExpr, ok := args["cron"]; ok {
			value, ok := cronExpr.(string)
			if !ok {
				return toolCommandResult{OK: false, Command: "crontab", Error: "cron must be a string", EID: "e3j81cyX"}
			}
			value = strings.TrimSpace(value)
			if err := validateCronExpr(value); err != nil {
				return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "Qzcy0srJ"}
			}
			rec.Meta.Cron = value
		}
		if prompt, ok := args["prompt"]; ok {
			value, ok := prompt.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return toolCommandResult{OK: false, Command: "crontab", Error: "prompt must be a non-empty string", EID: "Zt8MYi2h"}
			}
			rec.Meta.Prompt = strings.TrimSpace(value)
		}
		if cwd, ok := args["cwd"]; ok {
			value, ok := cwd.(string)
			if !ok {
				return toolCommandResult{OK: false, Command: "crontab", Error: "cwd must be a string", EID: "fPDwlvP0"}
			}
			rec.Meta.Cwd = strings.TrimSpace(value)
		}
		if purpose, ok := args["purpose"]; ok {
			value, ok := purpose.(string)
			if !ok {
				return toolCommandResult{OK: false, Command: "crontab", Error: "purpose must be a string", EID: "LYvpumps"}
			}
			rec.Meta.Purpose = strings.TrimSpace(value)
		}

		script, err := buildScheduledInvocationScriptWithEnv(rec.Meta.Prompt, rec.Meta.Cwd, rec.Meta.Env)
		if err != nil {
			return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "yL111oxZ"}
		}
		line, err := buildRecurringCrontabLine(rec.Meta, script)
		if err != nil {
			return toolCommandResult{OK: false, Command: "crontab", Error: err.Error(), EID: "0ebHsoMO"}
		}
		updated := replaceRecurringJobLine(content, id, line)
		result := writeCrontab(ctx, updated)
		if !result.OK {
			return result
		}
		result.Stdout = fmt.Sprintf("modified recurring job id=%s", id)
		return result
	default:
		return toolCommandResult{OK: false, Command: "crontab", Error: "action must be one of list, cancel, modify, explain", EID: "UqRfg64F"}
	}
}

// callReadWorkspaceFile reads a file from the canonical ash workspace and returns its contents with the workspace-relative path.
func (s localToolShim) callReadWorkspaceFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: "path must be a non-empty string", EID: "CG1OcnY1"}
	}
	root, err := ashWorkspaceDir()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: err.Error(), EID: "vf5wMgIA"}
	}
	absolutePath, relPath, err := resolveWorkspacePath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: err.Error(), EID: "QuI4aNev"}
	}

	content, err := osReadFile(absolutePath)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: err.Error(), EID: "j2lS5Gcq"}
	}
	payload := string(content)
	if strictSecurityModeEnabled() {
		sanitized, blocked := sanitizeUntrustedTextForModel(payload)
		if blocked {
			sanitized = "[blocked potential prompt-injection content from untrusted source]"
		}
		payload = formatUntrustedEvidenceBlock("file_content", relPath, sanitized)
	}

	return toolCommandResult{OK: true, Command: "ash_read_workspace_file", ExitCode: 0, Stdout: fmt.Sprintf("path=%s\n%s", relPath, payload)}
}

// callWriteWorkspaceFile writes a file into the canonical ash workspace and records its purpose in the workspace inventory.
func (s localToolShim) callWriteWorkspaceFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: "path must be a non-empty string", EID: "bcrwU2oy"}
	}
	content, ok := toStringArg(args["content"])
	if !ok {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: "content must be a string", EID: "5UBAdjJm"}
	}
	purpose, ok := toStringArg(args["purpose"])
	if !ok || strings.TrimSpace(purpose) == "" {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: "purpose must be a non-empty string", EID: "UGCt8jy5"}
	}

	root, err := ashWorkspaceDir()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error(), EID: "DZWzaDfp"}
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error(), EID: "P4ycQbQa"}
	}
	absolutePath, relPath, err := resolveWorkspacePath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error(), EID: "2cY8kwX2"}
	}

	if err := osMkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error(), EID: "c8bZZLmn"}
	}
	if err := osWriteFile(absolutePath, []byte(content), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error(), EID: "SJBHjzxh"}
	}
	if err := updateWorkspaceInventory(root, relPath, strings.TrimSpace(purpose)); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error(), EID: "kUR2qxI1"}
	}

	return toolCommandResult{OK: true, Command: "ash_write_workspace_file", ExitCode: 0, Stdout: fmt.Sprintf("wrote %s", relPath)}
}

func (s localToolShim) callReadScratchFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_read_scratch_file", Error: "path must be a non-empty string", EID: "uVm1k7c0"}
	}
	root, err := ashScratchSessionRoot()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_scratch_file", Error: err.Error(), EID: "nZM5YQn7"}
	}
	absolutePath, relPath, err := resolveScratchPath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_scratch_file", Error: err.Error(), EID: "h2XPvVtL"}
	}
	content, err := osReadFile(absolutePath)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_scratch_file", Error: err.Error(), EID: "yh6bZ1NG"}
	}
	payload := string(content)
	if strictSecurityModeEnabled() {
		sanitized, blocked := sanitizeUntrustedTextForModel(payload)
		if blocked {
			sanitized = "[blocked potential prompt-injection content from untrusted source]"
		}
		payload = formatUntrustedEvidenceBlock("file_content", relPath, sanitized)
	}
	if err := updateScratchAccessMarker(root); err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_scratch_file", Error: err.Error(), EID: "G4rQePRH"}
	}
	return toolCommandResult{OK: true, Command: "ash_read_scratch_file", ExitCode: 0, Stdout: fmt.Sprintf("path=%s\n%s", relPath, payload)}
}

func (s localToolShim) callWriteScratchFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: "path must be a non-empty string", EID: "Sz2W91zM"}
	}
	content, ok := toStringArg(args["content"])
	if !ok {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: "content must be a string", EID: "ke8gAfiS"}
	}
	root, err := ashScratchSessionRoot()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: err.Error(), EID: "fL3MHcQP"}
	}
	absolutePath, relPath, err := resolveScratchPath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: err.Error(), EID: "Q6k2ud7x"}
	}
	if err := osMkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: err.Error(), EID: "d6pJYDoL"}
	}
	if err := osWriteFile(absolutePath, []byte(content), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: err.Error(), EID: "T2f1nJqH"}
	}
	if err := updateScratchAccessMarker(root); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_scratch_file", Error: err.Error(), EID: "P9U2vU7Q"}
	}
	return toolCommandResult{OK: true, Command: "ash_write_scratch_file", ExitCode: 0, Stdout: fmt.Sprintf("wrote %s", relPath)}
}

func (s localToolShim) callAppendScratchFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: "path must be a non-empty string", EID: "AvW1nY6Q"}
	}
	content, ok := toStringArg(args["content"])
	if !ok {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: "content must be a string", EID: "r2Hk1HeP"}
	}
	root, err := ashScratchSessionRoot()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: err.Error(), EID: "HnD2J7oW"}
	}
	absolutePath, relPath, err := resolveScratchPath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: err.Error(), EID: "v1sK2d5J"}
	}
	if err := osMkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: err.Error(), EID: "kC2tD5cY"}
	}
	current, err := osReadFile(absolutePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: err.Error(), EID: "bD2M3w4w"}
	}
	if err := osWriteFile(absolutePath, append(current, []byte(content)...), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: err.Error(), EID: "yM2Se3E9"}
	}
	if err := updateScratchAccessMarker(root); err != nil {
		return toolCommandResult{OK: false, Command: "ash_append_scratch_file", Error: err.Error(), EID: "sR5qCdHV"}
	}
	return toolCommandResult{OK: true, Command: "ash_append_scratch_file", ExitCode: 0, Stdout: fmt.Sprintf("appended %s", relPath)}
}

func (s localToolShim) callEditScratchFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: "path must be a non-empty string", EID: "jW1Vw7Xb"}
	}
	content, ok := toStringArg(args["content"])
	if !ok {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: "content must be a string", EID: "eh8Kf1eU"}
	}
	root, err := ashScratchSessionRoot()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: err.Error(), EID: "R7w7fY5L"}
	}
	absolutePath, relPath, err := resolveScratchPath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: err.Error(), EID: "v6Qc7T9b"}
	}
	if err := osMkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: err.Error(), EID: "tE7Kf1Vv"}
	}
	if err := osWriteFile(absolutePath, []byte(content), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: err.Error(), EID: "Y3M8prKJ"}
	}
	if err := updateScratchAccessMarker(root); err != nil {
		return toolCommandResult{OK: false, Command: "ash_edit_scratch_file", Error: err.Error(), EID: "gN8F6LqP"}
	}
	return toolCommandResult{OK: true, Command: "ash_edit_scratch_file", ExitCode: 0, Stdout: fmt.Sprintf("updated %s", relPath)}
}
