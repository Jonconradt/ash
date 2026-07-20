package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
)

const (
	defaultOllamaPort = "11434"
	defaultHistoryMax = 40
	historyFileName   = ".ash_history.json"
	systemFileName    = ".ash_system"
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatResponse struct {
	Message message `json:"message"`
	Error   string  `json:"error"`
}

type historyData struct {
	Conversations map[string][]message `json:"conversations"`
}

var (
	markdownRenderer = renderMarkdownWithGlamour
	osGetwd          = os.Getwd
	osUserHomeDir    = os.UserHomeDir
	osReadFile       = os.ReadFile
	osWriteFile      = os.WriteFile
	newTermRenderer  = glamour.NewTermRenderer
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

	conversation := history.Conversations[historyKey]
	messages := make([]message, 0, len(conversation)+2)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, conversation...)
	messages = append(messages, message{Role: "user", Content: userInput})

	assistantReply, err := chat(baseURL, model, messages)
	if err != nil {
		fmt.Fprintf(stderr, "ollama request failed: %v\n", err)
		return 1
	}

	fmt.Fprint(stdout, formatAssistantOutput(assistantReply))

	conversation = append(conversation,
		message{Role: "user", Content: userInput},
		message{Role: "assistant", Content: assistantReply},
	)
	conversation = keepRecentMessages(conversation, historyLimit())
	history.Conversations[historyKey] = conversation

	if err := saveHistory(historyPath, history); err != nil {
		fmt.Fprintf(stderr, "warning: failed to save history: %v\n", err)
	}

	return 0
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
		return string(content), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	homePath := filepath.Join(home, systemFileName)
	if content, err := osReadFile(homePath); err == nil {
		return string(content), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	return "", nil
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

func keepRecentMessages(messages []message, max int) []message {
	if len(messages) <= max {
		return messages
	}

	return append([]message(nil), messages[len(messages)-max:]...)
}

func chat(baseURL, model string, messages []message) (string, error) {
	requestBody := chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}

	if parsed.Error != "" {
		return "", errors.New(parsed.Error)
	}

	return parsed.Message.Content, nil
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
