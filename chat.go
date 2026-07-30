package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type chatRequest struct {
	Model      string           `json:"model"`
	Messages   []message        `json:"messages"`
	Tools      []toolDefinition `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
	Stream     bool             `json:"stream"`
}

type chatResponse struct {
	Message message `json:"message"`
	Error   string  `json:"error"`
}

type chatStatusError struct {
	StatusCode int
	Body       string
}

// Error returns the HTTP status code and response body as a formatted error string.
func (e chatStatusError) Error() string {
	return fmt.Sprintf("status %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

type toolDefinition struct {
	Type     string                 `json:"type"`
	Function toolFunctionDefinition `json:"function"`
}

type toolFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function toolFunctionCall `json:"function"`
}

type toolFunctionCall struct {
	Index     *int           `json:"index,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

var cloudBusy503Messages = []string{
	"Cloud brain wandered off chasing a shiny thing. It is way too busy right now.",
	"The cloud model is juggling too many tabs and got distracted. Try again in a moment.",
	"Service is currently busy pretending to multitask. Give it another shot shortly.",
	"The model is overbooked and daydreaming at the same time. Please retry soon.",
	"503: the cloud got distracted mid-thought and is too busy to answer right now.",
	"Our cloud assistant is in a meeting that should have been an email. Try again soon.",
	"The model is currently swamped and staring into the middle distance. Retry in a bit.",
	"Cloud queue is full and the model is politely panicking. Please try again shortly.",
	"The service is busy speed-walking between tasks and forgot your question. Retry soon.",
	"The model is distracted by an urgent nothing and cannot chat right now. Try again soon.",
	"503 from the cloud: too busy, mildly frazzled, and temporarily unavailable.",
	"The cloud model is taking a tiny chaos break. Please try again in a minute.",
	"The model is currently overloaded and pretending it is fine. Retry shortly.",
	"Too much happening upstairs in the cloud right now. Give it another try soon.",
	"The service is busy and briefly out to lunch, mentally. Please retry in a moment.",
	"Cloud model status: distracted, overbooked, and not accepting new thoughts right now.",
	"503: the model is wearing too many hats and dropped this request. Try again soon.",
	"The cloud is busy doing cloud things and got sidetracked. Please retry shortly.",
	"The model is currently in maximum bustle mode. Give it another nudge in a bit.",
	"Service unavailable: distracted by shiny logs and far too busy at the moment.",
}

var cloudServer500Messages = []string{
	"Server hiccup: the wires are crossed and someone is rebooting the coffee machine.",
	"The server tripped over its own stack trace. Please try again in a moment.",
	"500 detected: backend gremlins are doing unauthorized maintenance.",
	"General server error: the engine sneezed and dropped a few gears.",
	"The server is currently having a dramatic monologue. Retry shortly.",
	"Internal error: the hamster wheel paused for an unscheduled break.",
	"Our server found a mysterious semicolon and needs a second attempt.",
	"500: the backend lost the plot, but only temporarily.",
	"Server confusion event: everything is technically on fire, politely.",
	"The request hit a pothole in the server room. Please try again soon.",
	"Internal server wobble. A quick retry usually fixes the vibe.",
	"The backend is untangling cables in existential mode. Retry in a bit.",
	"Server error: one subsystem blinked and everyone panicked.",
	"The server dropped this request while juggling dependencies.",
	"500 from upstream: we are sweeping up stack traces right now.",
	"General server fault: the robots are rebooting their confidence.",
	"The backend hit an oops and is patching itself together.",
	"Server trouble: a tiny outage with big main-character energy.",
	"Internal error: the logs are being read sternly by engineers.",
	"The server took a wrong turn at runtime. Please retry shortly.",
}

// randomCloudBusy503Message returns a humorous retry message for transient 503 service-busy responses.
func randomCloudBusy503Message() string {
	if len(cloudBusy503Messages) == 0 {
		return "The cloud model is distracted and too busy right now. Please try again shortly."
	}
	idx := int(timeNow().UnixNano() % int64(len(cloudBusy503Messages)))
	return cloudBusy503Messages[idx]
}

// randomCloudServer500Message returns a humorous retry message for transient 500 server errors.
func randomCloudServer500Message() string {
	if len(cloudServer500Messages) == 0 {
		return "The server hit an internal error. Please try again shortly."
	}
	idx := int(timeNow().UnixNano() % int64(len(cloudServer500Messages)))
	return cloudServer500Messages[idx]
}

// chat sends a chat request to the configured AI endpoint and returns the assistant response or an error.
func chat(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	configureDebugLogging()

	adapter, err := adapterForProvider(aiCfg.Provider)
	if err != nil {
		return chatResponse{}, err
	}

	payload, err := adapter.BuildPayload(aiCfg, messages, tools)
	if err != nil {
		return chatResponse{}, err
	}
	endpointURL := adapter.Endpoint(aiCfg.BaseURL)
	slog.Debug("AI request", "request_id", requestIDGenerator(), "url", endpointURL, "provider", adapter.Name(), "EID", "UqNZjp9I")
	slog.Debug("AI request payload", "request_id", requestIDGenerator(), "payload", string(payload), "EID", "aPkzWTCJ")

	attempts := retryMaxAttempts()
	baseDelay := retryBaseDelay()
	maxDelay := retryMaxDelay()
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payload))
		if err != nil {
			return chatResponse{}, err
		}
		adapter.ApplyHeaders(req, aiCfg)

		client := newHTTPClient(aiTimeout())
		resp, err := client.Do(req)
		if err != nil {
			if !shouldRetryAIError(err, attempt, attempts) {
				return chatResponse{}, err
			}
			slog.Debug("AI request attempt failed", "request_id", requestIDGenerator(), "attempt", attempt, "max_attempts", attempts, "error", err, "EID", "0s3aTomF")
			if err := sleepWithContext(ctx, backoffDelay(attempt, baseDelay, maxDelay)); err != nil {
				return chatResponse{}, err
			}
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return chatResponse{}, err
		}
		slog.Debug("AI response", "request_id", requestIDGenerator(), "status", resp.StatusCode, "body", string(body), "EID", "2D1hx03p")

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			statusErr := chatStatusError{StatusCode: resp.StatusCode, Body: string(body)}
			if !shouldRetryStatusCode(resp.StatusCode, attempt, attempts) {
				return chatResponse{}, statusErr
			}
			slog.Debug("AI request attempt status", "request_id", requestIDGenerator(), "attempt", attempt, "max_attempts", attempts, "status", resp.StatusCode, "EID", "VUGCSB86")
			if err := sleepWithContext(ctx, backoffDelay(attempt, baseDelay, maxDelay)); err != nil {
				return chatResponse{}, err
			}
			continue
		}

		parsed, err := adapter.ParseResponse(body)
		if err != nil {
			return chatResponse{}, err
		}

		if parsed.Error != "" {
			if !shouldRetryAIError(errors.New(parsed.Error), attempt, attempts) {
				return chatResponse{}, errors.New(parsed.Error)
			}
			slog.Debug("AI request attempt model error", "request_id", requestIDGenerator(), "attempt", attempt, "max_attempts", attempts, "error", parsed.Error, "EID", "wnrl8LcI")
			if err := sleepWithContext(ctx, backoffDelay(attempt, baseDelay, maxDelay)); err != nil {
				return chatResponse{}, err
			}
			continue
		}

		return parsed, nil
	}

	return chatResponse{}, errors.New("AI request failed after retries")
}

func shouldRetryAIError(err error, attempt, maxAttempts int) bool {
	if attempt >= maxAttempts {
		return false
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "temporarily unavailable") || strings.Contains(msg, "busy") || strings.Contains(msg, "connection") || strings.Contains(msg, "EOF") || strings.Contains(msg, "reset") || strings.Contains(msg, "refused")
}

func shouldRetryStatusCode(statusCode, attempt, maxAttempts int) bool {
	if attempt >= maxAttempts {
		return false
	}
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusConflict, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadGateway, http.StatusInternalServerError:
		return true
	default:
		return false
	}
}

func backoffDelay(attempt int, base, max time.Duration) time.Duration {
	if attempt <= 1 || base <= 0 {
		return 0
	}
	const maxDuration = time.Duration(1<<63 - 1)
	delay := base
	for step := 2; step < attempt; step++ {
		if max > 0 && delay >= max {
			return max
		}
		if delay > maxDuration/2 {
			if max > 0 {
				return max
			}
			return maxDuration
		}
		delay *= 2
	}
	if max > 0 && delay > max {
		return max
	}
	return delay
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}
