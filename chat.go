package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
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

// Error returns the error message.
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

// randomCloudBusy503Message returns the computed value for this helper.
func randomCloudBusy503Message() string {
	if len(cloudBusy503Messages) == 0 {
		return "The cloud model is distracted and too busy right now. Please try again shortly."
	}
	idx := int(uint64(timeNow().UnixNano()) % uint64(len(cloudBusy503Messages)))
	return cloudBusy503Messages[idx]
}

// randomCloudServer500Message returns the computed value for this helper.
func randomCloudServer500Message() string {
	if len(cloudServer500Messages) == 0 {
		return "The server hit an internal error. Please try again shortly."
	}
	idx := int(uint64(timeNow().UnixNano()) % uint64(len(cloudServer500Messages)))
	return cloudServer500Messages[idx]
}

// chat returns the computed value for this helper.
func chat(ctx context.Context, aiCfg aiConfig, messages []message, tools []toolDefinition) (chatResponse, error) {
	requestBody := chatRequest{
		Model:    aiCfg.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return chatResponse{}, err
	}
	debugLogf("AI request: url=%s/api/chat", aiCfg.BaseURL)
	debugLogf("AI request payload: %s", string(payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aiCfg.BaseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return chatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if aiCfg.Authorization != "" {
		req.Header.Set("Authorization", aiCfg.Authorization)
	}

	client := newHTTPClient(aiTimeout())
	resp, err := client.Do(req)
	if err != nil {
		return chatResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatResponse{}, err
	}
	debugLogf("AI response: status=%d body=%s", resp.StatusCode, string(body))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return chatResponse{}, chatStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return chatResponse{}, err
	}

	if parsed.Error != "" {
		return chatResponse{}, errors.New(parsed.Error)
	}

	return parsed, nil
}
