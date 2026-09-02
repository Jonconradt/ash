// Package model holds the wire-level chat/tool data types shared by the ash CLI
// (internal/app) and its provider adapters, kept dependency-free so either side
// can import it without creating an import cycle.
package model

// Message is a single chat turn (system/user/assistant/tool).
type Message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolName    string       `json:"tool_name,omitempty"`
	ToolCallID  string       `json:"tool_call_id,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is a binary file (image or document) attached to a message, either
// supplied by the user (--attach/@path) or returned by a tool result.
type Attachment struct {
	MimeType string `json:"mime_type"`
	FileName string `json:"file_name,omitempty"`
	Data     []byte `json:"-"`
}

// ChatRequest is the provider-agnostic request payload built for a chat call.
type ChatRequest struct {
	Model      string           `json:"model"`
	Messages   []Message        `json:"messages"`
	Tools      []ToolDefinition `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
	Stream     bool             `json:"stream"`
}

// ChatUsage reports token accounting for a single chat response, when available.
type ChatUsage struct {
	InputTokens  int
	OutputTokens int
	Available    bool
}

// ChatResponse is the provider-agnostic result of a chat call.
type ChatResponse struct {
	Message     Message      `json:"message"`
	Error       string       `json:"error"`
	Usage       ChatUsage    `json:"-"`
	Attachments []Attachment `json:"-"`
}

// ToolDefinition describes a callable tool offered to the model.
type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

// ToolFunctionDefinition is the function schema portion of a ToolDefinition.
type ToolFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function ToolFunctionCall `json:"function"`
}

// ToolFunctionCall is the function-call portion of a ToolCall.
type ToolFunctionCall struct {
	Index     *int           `json:"index,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}
