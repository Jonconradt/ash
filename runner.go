package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

var chatExecutor = chat

type executionTask struct {
	ID     int
	Goal   string
	Status string
	Detail string
}

type toolObservation struct {
	Command string
	OK      bool
	Summary string
}

const (
	taskStatusPending = "pending"
	taskStatusRunning = "running"
	taskStatusDone    = "done"
	taskStatusBlocked = "blocked"
)

// runToolLoop runs the requested operation.
func runToolLoop(ctx context.Context, aiCfg aiConfig, userInput string, messages []message, shim mcpToolShim) (string, []message, error) {
	configureDebugLogging()

	maxIters := maxToolIterations()
	tools := shim.ListTools()
	tasks := buildExecutionTasks(userInput, taskListMax())
	observations := make([]toolObservation, 0, 8)
	stallRounds := 0
	forcedToolRetryUsed := false
	slog.Debug("Tool loop started", "request_id", requestIDGenerator(), "max_iters", maxIters, "tools", len(tools), "EID", "kLt1nKGy")

	for i := 0; i <= maxIters; i++ {
		slog.Debug("Tool loop iteration", "request_id", requestIDGenerator(), "iteration", i+1, "message_count", len(messages), "EID", "K9mhqboH")
		roundMessages := append([]message{}, messages...)
		stateMessage := buildExecutionStateMessage(userInput, tasks, observations, relevanceWindow())
		if len(roundMessages) == 0 {
			roundMessages = append(roundMessages, stateMessage)
		} else {
			insertAt := len(roundMessages) - 1
			roundMessages = append(roundMessages[:insertAt], append([]message{stateMessage}, roundMessages[insertAt:]...)...)
		}

		response, err := chatExecutor(ctx, aiCfg, roundMessages, tools)
		if err != nil {
			return "", nil, err
		}

		assistant := response.Message
		if strings.TrimSpace(assistant.Role) == "" {
			assistant.Role = "assistant"
		}
		for j := range assistant.ToolCalls {
			if strings.TrimSpace(assistant.ToolCalls[j].Type) == "" {
				assistant.ToolCalls[j].Type = "function"
			}
			if assistant.ToolCalls[j].Function.Index == nil {
				idx := j
				assistant.ToolCalls[j].Function.Index = &idx
			}
		}
		messages = append(messages, assistant)

		if len(assistant.ToolCalls) == 0 {
			slog.Debug("Assistant returned no tool calls", "request_id", requestIDGenerator(), "EID", "lEPk12rd")
			if hasPendingExecutionTasks(tasks) {
				stallRounds++
			} else {
				stallRounds = 0
			}

			if !forcedToolRetryUsed && shouldForceToolRetry(userInput, assistant.Content, tools) {
				forcedToolRetryUsed = true
				slog.Debug("Execution-style prompt detected, forcing one retry with tool-use instruction", "request_id", requestIDGenerator(), "EID", "aDx9FvQa")
				messages = append(messages, message{
					Role:    "system",
					Content: "When a user asks to run or execute code/commands and tools are available, call an appropriate tool instead of only explaining.",
				})
				continue
			}

			if hasPendingExecutionTasks(tasks) && len(observations) > 0 && stallRounds < maxTaskStallRounds() {
				messages = append(messages, message{
					Role:    "system",
					Content: "Continue executing the pending tasks by calling available tools when possible.",
				})
				continue
			}
			return assistant.Content, messages, nil
		}

		stallRounds = 0

		if i == maxIters {
			return "", nil, fmt.Errorf("tool iteration limit reached (%d)", maxIters)
		}

		promoteNextPendingTask(tasks)
		for _, call := range assistant.ToolCalls {
			toolName := strings.TrimSpace(call.Function.Name)
			slog.Debug("Tool invocation requested", "request_id", requestIDGenerator(), "name", toolName, "args", marshalForDebug(call.Function.Arguments), "EID", "iYWCHf8N")
			toolResult := shim.CallTool(ctx, toolName, call.Function.Arguments)
			slog.Debug("Tool invocation result", "request_id", requestIDGenerator(), "name", toolName, "result", toolResult, "EID", "L6UuVgEs")
			observation := parseToolObservation(toolResult)
			if observation.Command == "" {
				observation.Command = toolName
			}
			observations = append(observations, observation)
			applyToolObservationToTasks(tasks, observation)
			messages = append(messages, message{
				Role:     "tool",
				Content:  toolResult,
				ToolName: toolName,
			})
		}
	}

	return "", nil, errors.New("unreachable tool loop state")
}

// shouldForceToolRetry reports whether the condition is true.
func shouldForceToolRetry(userInput, assistantContent string, tools []toolDefinition) bool {
	if len(tools) == 0 {
		return false
	}
	query := strings.ToLower(strings.TrimSpace(userInput))
	if query == "" {
		return false
	}

	markers := []string{"use python", "run python", "execute python", "run command", "execute command", "use tool", "execute tool"}
	for _, marker := range markers {
		if strings.Contains(query, marker) {
			return true
		}
	}

	assistantText := strings.ToLower(assistantContent)
	if strings.Contains(assistantText, "save the code") || strings.Contains(assistantText, "run this") {
		return true
	}

	return false
}

// buildExecutionTasks splits the user's request into execution tasks, honoring the configured task limit.
func buildExecutionTasks(userInput string, max int) []executionTask {
	normalized := strings.TrimSpace(userInput)
	if normalized == "" {
		return nil
	}

	parts := strings.Split(normalized, " and ")
	if len(parts) == 1 {
		parts = []string{normalized}
	}

	if max <= 0 {
		max = defaultTaskMax
	}

	tasks := make([]executionTask, 0, len(parts))
	for _, part := range parts {
		goal := strings.TrimSpace(strings.Trim(part, "?!. "))
		if goal == "" {
			continue
		}
		tasks = append(tasks, executionTask{
			ID:     len(tasks) + 1,
			Goal:   goal,
			Status: taskStatusPending,
		})
		if len(tasks) >= max {
			break
		}
	}

	if len(tasks) == 0 {
		return []executionTask{{ID: 1, Goal: normalized, Status: taskStatusPending}}
	}

	return tasks
}

// buildExecutionStateMessage constructs the system prompt that summarizes the current execution plan and recent tool observations.
func buildExecutionStateMessage(userInput string, tasks []executionTask, observations []toolObservation, window int) message {
	if window <= 0 {
		window = defaultRelevanceWin
	}

	var b strings.Builder
	b.WriteString("Execution task list (invocation-scoped):\n")
	b.WriteString("User request: ")
	b.WriteString(strings.TrimSpace(userInput))
	b.WriteString("\n")

	if len(tasks) == 0 {
		b.WriteString("- (no explicit tasks)\n")
	} else {
		for _, task := range tasks {
			b.WriteString(fmt.Sprintf("- [%s] #%d %s", task.Status, task.ID, task.Goal))
			if strings.TrimSpace(task.Detail) != "" {
				b.WriteString(": ")
				b.WriteString(strings.TrimSpace(task.Detail))
			}
			b.WriteString("\n")
		}
	}

	recent := observations
	if len(recent) > window {
		recent = recent[len(recent)-window:]
	}
	b.WriteString("Recent tool observations:\n")
	if len(recent) == 0 {
		b.WriteString("- (none yet)\n")
	} else {
		for _, obs := range recent {
			status := "ok"
			if !obs.OK {
				status = "error"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", status, strings.TrimSpace(obs.Command), strings.TrimSpace(obs.Summary)))
		}
	}

	b.WriteString("When tasks are pending, prefer tool calls over explanation-only replies.")

	return message{Role: "system", Content: b.String()}
}

// parseToolObservation converts a tool result payload into a compact observation suitable for task tracking.
func parseToolObservation(toolResult string) toolObservation {
	var parsed toolCommandResult
	if err := json.Unmarshal([]byte(toolResult), &parsed); err != nil {
		return toolObservation{Summary: strings.TrimSpace(toolResult)}
	}

	summary := strings.TrimSpace(parsed.Stdout)
	if summary == "" {
		summary = strings.TrimSpace(parsed.Stderr)
	}
	if summary == "" {
		summary = strings.TrimSpace(parsed.Error)
	}
	if summary == "" {
		summary = "(no output)"
	}

	if idx := strings.Index(summary, "\n"); idx >= 0 {
		summary = summary[:idx]
	}

	return toolObservation{
		Command: strings.TrimSpace(parsed.Command),
		OK:      parsed.OK,
		Summary: summary,
	}
}

// promoteNextPendingTask marks the next pending task as running so execution can proceed.
func promoteNextPendingTask(tasks []executionTask) {
	for i := range tasks {
		if tasks[i].Status == taskStatusPending {
			tasks[i].Status = taskStatusRunning
			return
		}
	}
}

// applyToolObservationToTasks updates the active execution task with the latest tool observation outcome.
func applyToolObservationToTasks(tasks []executionTask, observation toolObservation) {
	for i := range tasks {
		if tasks[i].Status != taskStatusPending && tasks[i].Status != taskStatusRunning {
			continue
		}
		tasks[i].Detail = strings.TrimSpace(observation.Summary)
		if observation.OK {
			tasks[i].Status = taskStatusDone
		} else {
			tasks[i].Status = taskStatusBlocked
		}
		return
	}
}

// hasPendingExecutionTasks reports whether any execution task is still pending or running.
func hasPendingExecutionTasks(tasks []executionTask) bool {
	for _, task := range tasks {
		if task.Status == taskStatusPending || task.Status == taskStatusRunning {
			return true
		}
	}
	return false
}
