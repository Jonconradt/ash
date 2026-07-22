package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/glamour"
)

const (
	defaultOllamaPort = "11434"
	defaultHistoryMax = 40
	defaultAITimeout  = 3 * time.Minute
	defaultToolTimeout = 15 * time.Second
	defaultToolOutputMax = 8192
	defaultMaxToolIters = 4
	historyFileName   = ".ash_history.json"
	systemFileName    = ".ash_system"
	toolsFileName     = ".ash_tools"
)

type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type chatRequest struct {
	Model    string           `json:"model"`
	Messages []message        `json:"messages"`
	Tools    []toolDefinition `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

type chatResponse struct {
	Message message `json:"message"`
	Error   string  `json:"error"`
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
	Function toolFunctionCall `json:"function"`
}

type toolFunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

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

type mcpToolShim interface {
	ListTools() []toolDefinition
	CallTool(ctx context.Context, name string, args map[string]any) string
}

type localToolShim struct {
	allowlist map[string]struct{}
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
	newTermRenderer     = glamour.NewTermRenderer
	signalNotifyContext = signal.NotifyContext
	newHTTPClient       = func(timeout time.Duration) *http.Client {
		return &http.Client{Timeout: timeout}
	}
	argumentBlockPattern = regexp.MustCompile(`(;|\|\||&&|\||` + "`" + `|\$\(|>|<|\x00|\n|\r)`)
	toolCommandRunner    = runToolCommand
	debugWriter          io.Writer = os.Stderr
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: ash <text>")
		return 1
	}

	aiURI := strings.TrimSpace(os.Getenv("AI"))
	if aiURI == "" {
		fmt.Fprintln(stderr, "AI environment variable is required (example: ollama://localhost/llama3.1)")
		return 1
	}

	baseURL, model, historyKey, err := parseAI(aiURI)
	if err != nil {
		fmt.Fprintf(stderr, "invalid AI value: %v\n", err)
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

	conversation := history.Conversations[historyKey]
	messages := make([]message, 0, len(conversation)+2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, conversation...)
	messages = append(messages, message{Role: "user", Content: userInput})

	timeout := aiTimeout()
	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stopSpinner := startThinkingIndicator(stderr)
	assistantReply, updatedMessages, err := runToolLoop(ctx, baseURL, model, userInput, messages, toolShim)
	stopSpinner()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "AI doesn't feel like talking right now. Try again later.")
			return 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "AI took longer than %s, so we should probably try again later\n", timeout)
			return 1
		}
		fmt.Fprintf(stderr, "ollama request failed: %v\n", err)
		return 1
	}

	fmt.Fprint(stdout, formatAssistantOutput(assistantReply))

	conversation = stripSystemMessage(updatedMessages)
	conversation = keepRecentMessages(conversation, historyLimit())
	history.Conversations[historyKey] = conversation

	if err := saveHistory(historyPath, history); err != nil {
		fmt.Fprintf(stderr, "warning: failed to save history: %v\n", err)
	}

	return 0
}

func runToolLoop(ctx context.Context, baseURL, model, userInput string, messages []message, shim mcpToolShim) (string, []message, error) {
	maxIters := maxToolIterations()
	tools := shim.ListTools()
	forcedToolRetryUsed := false
	debugLogf("Tool loop started: max_iters=%d tools=%d", maxIters, len(tools))

	for i := 0; i <= maxIters; i++ {
		debugLogf("Tool loop iteration=%d message_count=%d", i+1, len(messages))
		response, err := chat(ctx, baseURL, model, messages, tools)
		if err != nil {
			return "", nil, err
		}

		assistant := response.Message
		if strings.TrimSpace(assistant.Role) == "" {
			assistant.Role = "assistant"
		}
		messages = append(messages, assistant)

		if len(assistant.ToolCalls) == 0 {
			debugLogf("Assistant returned no tool calls")
			if !forcedToolRetryUsed && shouldForceToolRetry(userInput, assistant.Content, tools) {
				forcedToolRetryUsed = true
				debugLogf("Execution-style prompt detected, forcing one retry with tool-use instruction")
				messages = append(messages, message{
					Role: "system",
					Content: "When a user asks to run or execute code/commands and tools are available, call an appropriate tool instead of only explaining.",
				})
				continue
			}
			return assistant.Content, messages, nil
		}

		if i == maxIters {
			return "", nil, fmt.Errorf("tool iteration limit reached (%d)", maxIters)
		}

		for _, call := range assistant.ToolCalls {
			toolName := strings.TrimSpace(call.Function.Name)
			debugLogf("Tool invocation requested: name=%s args=%s", toolName, marshalForDebug(call.Function.Arguments))
			toolResult := shim.CallTool(ctx, toolName, call.Function.Arguments)
			debugLogf("Tool invocation result: name=%s result=%s", toolName, toolResult)
			messages = append(messages, message{
				Role:     "tool",
				Content:  toolResult,
				ToolName: toolName,
			})
		}
	}

	return "", nil, errors.New("unreachable tool loop state")
}

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

func verboseLoggingEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("ASH_VERBOSE")))
	switch raw {
	case "1", "true", "yes", "on", "debug":
		return true
	default:
		return false
	}
}

func debugLogf(format string, args ...any) {
	if !verboseLoggingEnabled() {
		return
	}
	if debugWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(debugWriter, "[ash-debug] "+format+"\n", args...)
}

func marshalForDebug(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<marshal-error:%s>", sanitizeJSONError(err.Error()))
	}
	return string(encoded)
}

func sortedAllowlist(allowlist map[string]struct{}) []string {
	out := make([]string, 0, len(allowlist))
	for name := range allowlist {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func stripSystemMessage(messages []message) []message {
	if len(messages) == 0 {
		return nil
	}
	if messages[0].Role == "system" {
		return append([]message(nil), messages[1:]...)
	}
	return append([]message(nil), messages...)
}

func parseAI(value string) (baseURL string, model string, historyKey string, err error) {
	u, err := url.Parse(value)
	if err != nil {
		return "", "", "", err
	}

	if u.Scheme != "ollama" {
		return "", "", "", errors.New("scheme must be ollama")
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", "", "", errors.New("host is required")
	}

	port := strings.TrimSpace(u.Port())
	if port == "" {
		port = defaultOllamaPort
	}

	model = strings.Trim(u.Path, "/")
	if model == "" {
		return "", "", "", errors.New("model is required in path")
	}

	baseURL = fmt.Sprintf("http://%s:%s", host, port)
	historyKey = fmt.Sprintf("%s/%s", baseURL, model)

	return baseURL, model, historyKey, nil
}

func readSystemPrompt() (string, error) {
	cwd, err := osGetwd()
	if err != nil {
		return "", err
	}

	cwdPath := filepath.Join(cwd, systemFileName)
	if content, err := osReadFile(cwdPath); err == nil {
		return expandSystemPrompt(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	homePath := filepath.Join(home, systemFileName)
	if content, err := osReadFile(homePath); err == nil {
		return expandSystemPrompt(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	return "", nil
}

func loadAllowlistedCommands() (map[string]struct{}, error) {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_ALLOWLIST")); raw != "" {
		return parseAllowlistCSV(raw), nil
	}

	cwd, err := osGetwd()
	if err != nil {
		return nil, err
	}

	cwdPath := filepath.Join(cwd, toolsFileName)
	if content, err := osReadFile(cwdPath); err == nil {
		return parseAllowlistFile(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return nil, err
	}

	homePath := filepath.Join(home, toolsFileName)
	if content, err := osReadFile(homePath); err == nil {
		return parseAllowlistFile(string(content)), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return map[string]struct{}{}, nil
}

func parseAllowlistCSV(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		name := normalizeToolName(part)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func parseAllowlistFile(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, token := range strings.Split(trimmed, ",") {
			name := normalizeToolName(token)
			if name == "" {
				continue
			}
			set[name] = struct{}{}
		}
	}
	return set
}

func normalizeToolName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

func expandSystemPrompt(prompt string) string {
	unameValue := ""
	if _, err := execLookPath("uname"); err == nil {
		if output, err := execCommandOutput("uname", "-a"); err == nil {
			unameValue = strings.TrimSpace(string(output))
		}
	}

	return os.Expand(prompt, func(key string) string {
		if key == "UNAME" && unameValue != "" {
			return unameValue
		}
		return os.Getenv(key)
	})
}

func getHistoryPath() (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, historyFileName), nil
}

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

func saveHistory(path string, data historyData) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return osWriteFile(path, content, 0o600)
}

func historyLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_HISTORY_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultHistoryMax
}

func aiTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("AI_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultAITimeout
}

func toolTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolTimeout
}

func toolOutputLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_OUTPUT_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolOutputMax
}

func maxToolIterations() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_MAX_TOOL_ITERS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return defaultMaxToolIters
}

func keepRecentMessages(messages []message, max int) []message {
	if len(messages) <= max {
		return messages
	}

	return append([]message(nil), messages[len(messages)-max:]...)
}

func chat(ctx context.Context, baseURL, model string, messages []message, tools []toolDefinition) (chatResponse, error) {
	requestBody := chatRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return chatResponse{}, err
	}
	debugLogf("AI request: url=%s/api/chat", baseURL)
	debugLogf("AI request payload: %s", string(payload))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return chatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

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
		return chatResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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

func (s localToolShim) ListTools() []toolDefinition {
	return []toolDefinition{
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "run_unix_command",
				Description: "Run a single allowlisted Unix executable with direct args and no shell expansion",
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
				Name:        "run_python3",
				Description: "Execute Python 3 code via python3 -c and return stdout/stderr",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{
							"type":        "string",
							"description": "Python code to execute",
						},
						"argv": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "string",
							},
							"description": "Optional argv values visible to the script as sys.argv[1:]",
						},
					},
					"required": []string{"code"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDefinition{
				Name:        "python3",
				Description: "Execute Python 3 code via python3 -c and return stdout/stderr",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{
							"type":        "string",
							"description": "Python code to execute",
						},
						"argv": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "string",
							},
							"description": "Optional argv values visible to the script as sys.argv[1:]",
						},
					},
					"required": []string{"code"},
				},
			},
		},
	}
}

func (s localToolShim) CallTool(ctx context.Context, name string, args map[string]any) string {
	var result toolCommandResult

	switch name {
	case "run_unix_command":
		result = s.callUnixCommand(ctx, args)
	case "run_python3", "python3":
		result = s.callPython3(ctx, args)
	default:
		result = toolCommandResult{OK: false, Error: fmt.Sprintf("unknown tool: %s", name)}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":"failed to encode tool result: %s"}`, sanitizeJSONError(err.Error()))
	}

	return string(encoded)
}

func (s localToolShim) callUnixCommand(ctx context.Context, args map[string]any) toolCommandResult {
	commandName, ok := toStringArg(args["command"])
	if !ok {
		return toolCommandResult{OK: false, Error: "command must be a string"}
	}

	commandName = normalizeToolName(commandName)
	if commandName == "" {
		return toolCommandResult{OK: false, Error: "command must be a bare executable name"}
	}

	if _, allowed := s.allowlist[commandName]; !allowed {
		return toolCommandResult{OK: false, Command: commandName, Error: "command is not allowlisted"}
	}

	argv, err := toStringSliceArg(args["args"])
	if err != nil {
		return toolCommandResult{OK: false, Command: commandName, Error: err.Error()}
	}

	for _, arg := range argv {
		if isBlockedArgument(arg) {
			return toolCommandResult{OK: false, Command: commandName, Error: "argument contains blocked shell control pattern"}
		}
	}

	return toolCommandRunner(ctx, commandName, argv, toolTimeout(), toolOutputLimit())
}

func (s localToolShim) callPython3(ctx context.Context, args map[string]any) toolCommandResult {
	code, ok := toStringArg(args["code"])
	if !ok || strings.TrimSpace(code) == "" {
		return toolCommandResult{OK: false, Command: "python3", Error: "code must be a non-empty string"}
	}

	argv, err := toStringSliceArg(args["argv"])
	if err != nil {
		return toolCommandResult{OK: false, Command: "python3", Error: err.Error()}
	}

	for _, arg := range argv {
		if isBlockedArgument(arg) {
			return toolCommandResult{OK: false, Command: "python3", Error: "argv contains blocked shell control pattern"}
		}
	}

	pythonArgs := append([]string{"-c", code}, argv...)
	return toolCommandRunner(ctx, "python3", pythonArgs, toolTimeout(), toolOutputLimit())
}

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

func truncateForToolOutput(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "\n...truncated..."
}

func toStringArg(value any) (string, bool) {
	v, ok := value.(string)
	if !ok {
		return "", false
	}
	return v, true
}

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
		out = append(out, v)
	}

	return out, nil
}

func isBlockedArgument(arg string) bool {
	return argumentBlockPattern.MatchString(arg)
}

func sanitizeJSONError(value string) string {
	value = strings.ReplaceAll(value, `"`, `'`)
	return strings.ReplaceAll(value, "\n", " ")
}

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

func formatAssistantOutput(raw string) string {
	rendered, err := markdownRenderer(raw)
	if err != nil {
		return ensureSingleTrailingNewline(raw)
	}

	return ensureSingleTrailingNewline(rendered)
}

func ensureSingleTrailingNewline(value string) string {
	trimmed := strings.TrimRight(value, "\n")
	return trimmed + "\n"
}
