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
	"io/fs"
	"net"
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
	defaultHistoryMax                 = 40
	defaultAITimeout                  = 3 * time.Minute
	defaultToolTimeout                = 15 * time.Second
	defaultToolOutputMax              = 8192
	defaultMaxToolIters               = 4
	defaultTaskMax                    = 6
	defaultRelevanceWin               = 4
	defaultStallRounds                = 2
	defaultLaunchdTimeout             = 15 * time.Second
	defaultCronTimeout                = 15 * time.Second
	defaultSchedulerLogMaxBytes int64 = 1 << 20
	defaultHistoryRetention           = 14 * 24 * time.Hour
	defaultHistoryCleanupBudget       = 300 * time.Millisecond
	sessionIDEnvName                  = "SESSION_ID"
	scheduledTaskEnvName              = "ASH_SCHEDULED_TASK"
	historyDirName                    = "history"
	systemFileName                    = ".ash_system"
	toolsFileName                     = ".ash_tools"
	ashWorkspaceDirName               = ".ash"
	inventoryFileName                 = "inventory.md"
	schedulerLogDirName               = "logs"
	launchAgentsDirName               = "launchagents"
	futurePromptAgentPrefix           = "com.user.gonetwork"
	jobMarkerPrefix                   = "# ash:job "
	installStartMarker                = "# >>> ash install >>>"
	installEndMarker                  = "# <<< ash install <<<"
)

var sessionIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9]+`)

type endpointPreset struct {
	Name string
	URL  string
}

var installEndpointPresets = []endpointPreset{
	{Name: "Ollama (local)", URL: "http://localhost:11434"},
	{Name: "Ollama (cloud)", URL: "https://ollama.com"},
	{Name: "OpenAI", URL: "https://api.openai.com/v1"},
	{Name: "Anthropic", URL: "https://api.anthropic.com/v1"},
	{Name: "Google Gemini (OpenAI-compatible)", URL: "https://generativelanguage.googleapis.com/v1beta/openai/"},
	{Name: "HuggingFace Router (OpenAI-compatible)", URL: "https://router.huggingface.co/v1"},
}

const (
	aiEnvEndpoint  = "AI_ENDPOINT"
	aiEnvModel     = "AI_MODEL"
	aiEnvAuthType  = "AI_AUTH_TYPE"
	aiEnvAuthToken = "AI_AUTH_TOKEN"
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

type aiConfig struct {
	BaseURL       string
	Model         string
	HistoryKey    string
	Authorization string
}

type toolFunctionCall struct {
	Index     *int           `json:"index,omitempty"`
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

const (
	taskStatusPending = "pending"
	taskStatusRunning = "running"
	taskStatusDone    = "done"
	taskStatusBlocked = "blocked"
)

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
)

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

// main is the program entry point.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run runs the requested operation.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		printUsage(stderr)
		return 1
	}

	if args[0] == "install" {
		return runInstall(args[1:], stdout, stderr)
	}

	if _, err := ensureSessionID(); err != nil {
		fmt.Fprintf(stderr, "failed to initialize SESSION_ID: %v\n", err)
		return 1
	}

	configureDebugLogging()
	defer cleanupHistoryRetention(defaultHistoryRetention, defaultHistoryCleanupBudget)

	if recommendation, err := installRecommendation(); err == nil && recommendation != "" {
		fmt.Fprintln(stderr, recommendation)
	}

	aiCfg, err := parseAIConfigFromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "invalid AI configuration: %v\n", err)
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
	systemPrompt = buildSystemPrompt(systemPrompt, timeNow())

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

	conversation := history.Conversations[aiCfg.HistoryKey]
	messages := make([]message, 0, len(conversation)+2)
	messages = append(messages, message{Role: "system", Content: systemPrompt})
	messages = append(messages, conversation...)
	messages = append(messages, message{Role: "user", Content: userInput})

	timeout := aiTimeout()
	ctx, stop := signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stopSpinner := startThinkingIndicator(stderr)
	assistantReply, updatedMessages, err := runToolLoop(ctx, aiCfg, userInput, messages, toolShim)
	stopSpinner()
	if err != nil {
		debugLogf("run failed: %v", err)
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "AI doesn't feel like talking right now. Try again later.")
			return 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "AI took longer than %s, so we should probably try again later\n", timeout)
			return 1
		}
		var statusErr chatStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusServiceUnavailable:
				fmt.Fprintln(stderr, pickCloudBusy503Message())
				return 1
			case http.StatusInternalServerError:
				fmt.Fprintln(stderr, pickCloudServer500Message())
				return 1
			}
		}
		fmt.Fprintf(stderr, "ollama request failed: %v\n", err)
		return 1
	}

	debugLogf("assistant final reply: %q", assistantReply)
	fmt.Fprint(stdout, formatAssistantOutput(assistantReply))

	conversation = stripSystemMessage(updatedMessages)
	conversation = keepRecentMessages(conversation, historyLimit())
	history.Conversations[aiCfg.HistoryKey] = conversation

	if err := saveHistory(historyPath, history); err != nil {
		fmt.Fprintf(stderr, "warning: failed to save history: %v\n", err)
	}

	return 0
}

// printUsage returns the computed value for this helper.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ash <text>")
	fmt.Fprintln(w, "       ash install [--shell bash|zsh] [--dry-run]")
}

// runInstall runs the requested operation.
func runInstall(args []string, stdout, stderr io.Writer) int {
	shellName, dryRun, err := parseInstallArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		printUsage(stderr)
		return 1
	}

	if shellName == "" {
		shellName = detectShellName(os.Getenv("SHELL"))
		if shellName == "" {
			shellName = "bash"
		}
	}

	rcPath, err := rcPathForShell(shellName)
	if err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}

	block := installSourceBlockForShell(shellName)
	if block == "" {
		fmt.Fprintf(stderr, "install error: unsupported shell %q\n", shellName)
		return 1
	}
	if err := ensureInstallShellWrapper(shellName, dryRun, stdout); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}
	if err := ensureBashProfileSourcing(shellName, dryRun, stdout); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}

	existing, err := readFileIfExists(rcPath)
	if err != nil {
		fmt.Fprintf(stderr, "install error: failed to read %s: %v\n", rcPath, err)
		return 1
	}

	existingBlock, hasManagedBlock := extractManagedInstallBlock(existing)
	if hasManagedBlock {
		if strings.TrimSpace(existingBlock) == strings.TrimSpace(block) {
			if err := finalizeInstallWorkspace(); err != nil {
				fmt.Fprintf(stderr, "install error: %v\n", err)
				return 1
			}
			if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
				fmt.Fprintf(stderr, "install error: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "ash install already present in %s\n", rcPath)
			fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
			return 0
		}

		updated, replaced := replaceManagedInstallBlock(existing, block)
		if !replaced {
			fmt.Fprintf(stderr, "install error: failed to update managed block in %s\n", rcPath)
			return 1
		}

		if dryRun {
			fmt.Fprintf(stdout, "[dry-run] would update install block in %s\n", rcPath)
			fmt.Fprint(stdout, block)
			if !strings.HasSuffix(block, "\n") {
				fmt.Fprintln(stdout)
			}
			return 0
		}

		if err := osWriteFile(rcPath, []byte(updated), 0o600); err != nil {
			fmt.Fprintf(stderr, "install error: failed to write %s: %v\n", rcPath, err)
			return 1
		}
		if err := finalizeInstallWorkspace(); err != nil {
			fmt.Fprintf(stderr, "install error: %v\n", err)
			return 1
		}
		if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
			fmt.Fprintf(stderr, "install error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "ash install updated wrappers in %s\n", rcPath)
		fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
		fmt.Fprintln(stdout, "restart your shell or source your rc file to activate wrappers")
		return 0
	}

	updated := appendInstallBlock(existing, block)
	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would append install block to %s\n", rcPath)
		fmt.Fprint(stdout, block)
		if !strings.HasSuffix(block, "\n") {
			fmt.Fprintln(stdout)
		}
		return 0
	}

	if err := osWriteFile(rcPath, []byte(updated), 0o600); err != nil {
		fmt.Fprintf(stderr, "install error: failed to write %s: %v\n", rcPath, err)
		return 1
	}
	if err := finalizeInstallWorkspace(); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}
	if err := maybeConfigureInstallEnv(stdout, stderr, dryRun); err != nil {
		fmt.Fprintf(stderr, "install error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "ash install appended wrappers to %s\n", rcPath)
	fmt.Fprintln(stdout, "synced .ash_system/.ash_tools to ~/.ash when present")
	fmt.Fprintln(stdout, "restart your shell or source your rc file to activate wrappers")
	return 0
}

// maybeConfigureInstallEnv returns the computed value for this helper.
func maybeConfigureInstallEnv(stdout, stderr io.Writer, dryRun bool) error {
	if dryRun {
		return nil
	}

	shouldConfigure, err := shouldConfigureInstallEnv()
	if err != nil {
		return err
	}
	if !shouldConfigure || !shouldPromptInstallEnv() {
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	values, err := promptInstallEnvValues(reader, stdout, stderr)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}

	path, err := ashEnvFilePath()
	if err != nil {
		return err
	}
	if err := osMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	if err := osWriteFile(path, []byte(buildManagedAshEnv(values)), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "updated %s\n", path)
	return nil
}

// shouldConfigureInstallEnv reports whether the condition is true.
func shouldConfigureInstallEnv() (bool, error) {
	if hasRequiredInstallEnvValues() {
		return false, nil
	}

	path, err := ashEnvFilePath()
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else {
		return false, err
	}
}

// hasRequiredInstallEnvValues reports whether the condition is true.
func hasRequiredInstallEnvValues() bool {
	endpoint := strings.TrimSpace(os.Getenv(aiEnvEndpoint))
	if endpoint == "" {
		return false
	}

	model := strings.TrimSpace(os.Getenv(aiEnvModel))
	if model == "" {
		return false
	}

	_, host, _, err := parseAIEndpoint(endpoint)
	if err != nil {
		return false
	}

	if !isCloudAIHost(host) {
		return true
	}

	authType := strings.ToLower(strings.TrimSpace(os.Getenv(aiEnvAuthType)))
	authToken := strings.TrimSpace(os.Getenv(aiEnvAuthToken))
	return authType == "bearer" && authToken != ""
}

// shouldPromptInstallEnv reports whether the condition is true.
func shouldPromptInstallEnv() bool {
	if runningInCI() {
		return false
	}
	stdinInfo, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	stdoutInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (stdinInfo.Mode()&os.ModeCharDevice != 0) && (stdoutInfo.Mode()&os.ModeCharDevice != 0)
}

// runningInCI runs the requested operation.
func runningInCI() bool {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "BUILD_BUILDID", "JENKINS_URL", "BUILDKITE"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

// ashEnvFilePath returns the computed value for this helper.
func ashEnvFilePath() (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".ash_env"), nil
}

// promptInstallEnvValues prompts for and returns user input.
func promptInstallEnvValues(reader *bufio.Reader, stdout, stderr io.Writer) (map[string]string, error) {
	fmt.Fprintln(stdout, "Configure ash environment values")
	endpoint, err := promptEndpointWithPresets(reader, stdout)
	if err != nil {
		return nil, err
	}
	model, err := promptNonEmpty(reader, stdout, aiEnvModel)
	if err != nil {
		return nil, err
	}

	_, host, _, err := parseAIEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	cloud := isCloudAIHost(host)

	authType := ""
	authToken := ""
	if cloud {
		authType = "bearer"
		authToken, err = promptNonEmpty(reader, stdout, aiEnvAuthToken)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(stderr, "selected cloud endpoint; using AI_AUTH_TYPE=bearer")
	} else {
		optionalToken, promptErr := promptOptional(reader, stdout, aiEnvAuthToken+" (optional for localhost)")
		if promptErr != nil {
			return nil, promptErr
		}
		if optionalToken != "" {
			authType = "bearer"
			authToken = optionalToken
		}
	}

	values := map[string]string{
		aiEnvEndpoint: endpoint,
		aiEnvModel:    model,
	}
	if authType != "" {
		values[aiEnvAuthType] = authType
	}
	if authToken != "" {
		values[aiEnvAuthToken] = authToken
	}
	return values, nil
}

// promptEndpointWithPresets prompts for and returns user input.
func promptEndpointWithPresets(reader *bufio.Reader, stdout io.Writer) (string, error) {
	fmt.Fprintln(stdout, "Select AI endpoint preset or enter a custom URL:")
	for i, preset := range installEndpointPresets {
		fmt.Fprintf(stdout, "  %d) %s - %s\n", i+1, preset.Name, preset.URL)
	}

	for {
		fmt.Fprintf(stdout, "%s: ", aiEnvEndpoint)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if idx, convErr := strconv.Atoi(input); convErr == nil {
			if idx >= 1 && idx <= len(installEndpointPresets) {
				return installEndpointPresets[idx-1].URL, nil
			}
		}
		if _, _, _, parseErr := parseAIEndpoint(input); parseErr == nil {
			return strings.TrimRight(input, "/"), nil
		}
		fmt.Fprintln(stdout, "invalid endpoint, enter a preset number or full http(s) URL")
	}
}

// promptNonEmpty prompts for and returns user input.
func promptNonEmpty(reader *bufio.Reader, stdout io.Writer, key string) (string, error) {
	for {
		fmt.Fprintf(stdout, "%s: ", key)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(line)
		if value != "" {
			return value, nil
		}
	}
}

// promptOptional prompts for and returns user input.
func promptOptional(reader *bufio.Reader, stdout io.Writer, key string) (string, error) {
	fmt.Fprintf(stdout, "%s: ", key)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// buildManagedAshEnv builds and returns a derived value.
func buildManagedAshEnv(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# managed by ash install\n")
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("export %s=%s\n", key, shellQuote(values[key])))
	}
	return b.String()
}

// finalizeInstallWorkspace returns the computed value for this helper.
func finalizeInstallWorkspace() error {
	if err := syncCanonicalConfigFilesFromCWD(); err != nil {
		return err
	}
	if err := hardenAshWorkspacePermissions(); err != nil {
		return err
	}
	return nil
}

// syncCanonicalConfigFilesFromCWD returns the computed value for this helper.
func syncCanonicalConfigFilesFromCWD() error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return err
	}

	cwd, err := osGetwd()
	if err != nil {
		return err
	}

	for _, name := range []string{systemFileName, toolsFileName} {
		srcPath := filepath.Join(cwd, name)
		content, readErr := osReadFile(srcPath)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("failed to read %s: %w", srcPath, readErr)
		}
		dstPath := filepath.Join(root, name)
		if writeErr := osWriteFile(dstPath, content, 0o600); writeErr != nil {
			return fmt.Errorf("failed to write %s: %w", dstPath, writeErr)
		}
	}

	return nil
}

// hardenAshWorkspacePermissions returns the computed value for this helper.
func hardenAshWorkspacePermissions() error {
	root, err := ashWorkspaceDir()
	if err != nil {
		return err
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return err
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil
		}

		if d.IsDir() {
			return os.Chmod(path, 0o700)
		}
		if mode.IsRegular() {
			return os.Chmod(path, 0o600)
		}
		return nil
	})
}

// parseInstallArgs parses and validates input values.
func parseInstallArgs(args []string) (shellName string, dryRun bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--shell":
			i++
			if i >= len(args) {
				return "", false, errors.New("--shell requires a value")
			}
			shellName = strings.TrimSpace(strings.ToLower(args[i]))
		default:
			return "", false, fmt.Errorf("unknown install argument: %s", args[i])
		}
	}
	return shellName, dryRun, nil
}

// detectShellName detects and returns the matching shell name.
func detectShellName(shellPath string) string {
	base := strings.TrimSpace(filepath.Base(shellPath))
	switch base {
	case "bash", "zsh":
		return base
	default:
		return ""
	}
}

// rcPathForShell returns the computed value for this helper.
func rcPathForShell(shellName string) (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}

	switch shellName {
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (supported: bash, zsh)", shellName)
	}
}

// readFileIfExists reads data from the filesystem.
func readFileIfExists(path string) (string, error) {
	content, err := osReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// appendInstallBlock appends content and returns the updated result.
func appendInstallBlock(existing, block string) string {
	if existing == "" {
		return block + "\n"
	}

	updated := existing
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += "\n" + block + "\n"
	return updated
}

// extractManagedInstallBlock extracts the managed block from the provided content.
func extractManagedInstallBlock(content string) (string, bool) {
	start := strings.Index(content, installStartMarker)
	if start < 0 {
		return "", false
	}
	endRel := strings.Index(content[start:], installEndMarker)
	if endRel < 0 {
		return "", false
	}
	end := start + endRel + len(installEndMarker)
	return content[start:end], true
}

// replaceManagedInstallBlock replaces content and returns the updated result.
func replaceManagedInstallBlock(existing, block string) (string, bool) {
	start := strings.Index(existing, installStartMarker)
	if start < 0 {
		return "", false
	}
	endRel := strings.Index(existing[start:], installEndMarker)
	if endRel < 0 {
		return "", false
	}
	end := start + endRel + len(installEndMarker)

	prefix := existing[:start]
	suffix := existing[end:]
	suffix = strings.TrimPrefix(suffix, "\n")

	var b strings.Builder
	b.WriteString(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(block)
	b.WriteString("\n")
	if strings.TrimSpace(suffix) != "" {
		b.WriteString("\n")
		b.WriteString(suffix)
	}
	return b.String(), true
}

// installRecommendation returns the computed value for this helper.
func installRecommendation() (string, error) {
	shellName := detectShellName(os.Getenv("SHELL"))
	if shellName == "" {
		return "", nil
	}

	if shellName == "bash" {
		installedViaProfile, err := bashInstalledViaProfileSourcing()
		if err != nil {
			return "", err
		}
		if installedViaProfile {
			return "", nil
		}
	}

	rcPath, err := rcPathForShell(shellName)
	if err != nil {
		return "", err
	}

	content, err := readFileIfExists(rcPath)
	if err != nil {
		return "", err
	}

	expected := installSourceBlockForShell(shellName)
	if existing, ok := extractManagedInstallBlock(content); ok {
		if strings.TrimSpace(existing) == strings.TrimSpace(expected) {
			return "", nil
		}
		return fmt.Sprintf("ash install for %s is outdated. Run: ash install --shell %s", shellName, shellName), nil
	}

	return fmt.Sprintf("ash is not installed for %s. Run: ash install --shell %s", shellName, shellName), nil
}

// bashInstalledViaProfileSourcing returns the computed value for this helper.
func bashInstalledViaProfileSourcing() (bool, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return false, err
	}

	profilePath := filepath.Join(home, ".bash_profile")
	profileContent, err := readFileIfExists(profilePath)
	if err != nil {
		return false, err
	}
	if !strings.Contains(profileContent, ".ash/.ash_bashrc") {
		return false, nil
	}

	wrapperPath, err := installShellWrapperPath("bash")
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(wrapperPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// installSourceBlockForShell returns the computed value for this helper.
func installSourceBlockForShell(shellName string) string {
	scriptName := ""
	switch shellName {
	case "bash":
		scriptName = ".ash_bashrc"
	case "zsh":
		scriptName = ".ash_zshrc"
	default:
		return ""
	}

	return strings.TrimSpace(`
` + installStartMarker + `
[ -f "$HOME/.ash/.ash_env" ] && . "$HOME/.ash/.ash_env"
[ -f "$HOME/.ash/` + scriptName + `" ] && . "$HOME/.ash/` + scriptName + `"
` + installEndMarker)
}

// installShellWrapperPath returns the computed value for this helper.
func installShellWrapperPath(shellName string) (string, error) {
	root, err := ashWorkspaceDir()
	if err != nil {
		return "", err
	}

	fileName := ""
	switch shellName {
	case "bash":
		fileName = ".ash_bashrc"
	case "zsh":
		fileName = ".ash_zshrc"
	default:
		return "", fmt.Errorf("unsupported shell %q", shellName)
	}
	return filepath.Join(root, fileName), nil
}

// ensureInstallShellWrapper ensures required state exists and is up to date.
func ensureInstallShellWrapper(shellName string, dryRun bool, stdout io.Writer) error {
	content := installBlockForShell(shellName)
	if content == "" {
		return fmt.Errorf("unsupported shell %q", shellName)
	}

	path, err := installShellWrapperPath(shellName)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would write shell wrapper file %s\n", path)
		return nil
	}

	if err := osMkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return osWriteFile(path, []byte(content+"\n"), 0o600)
}

// ensureBashProfileSourcing ensures required state exists and is up to date.
func ensureBashProfileSourcing(shellName string, dryRun bool, stdout io.Writer) error {
	if shellName != "bash" {
		return nil
	}
	home, err := osUserHomeDir()
	if err != nil {
		return err
	}
	profilePath := filepath.Join(home, ".bash_profile")
	line := `[ -f "$HOME/.ash/.ash_bashrc" ] && . "$HOME/.ash/.ash_bashrc"`

	existing, err := readFileIfExists(profilePath)
	if err != nil {
		return err
	}
	if strings.Contains(existing, line) {
		return nil
	}

	if dryRun {
		fmt.Fprintf(stdout, "[dry-run] would append ash source line to %s\n", profilePath)
		return nil
	}

	updated := existing
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += line + "\n"
	return osWriteFile(profilePath, []byte(updated), 0o600)
}

// installBlockForShell returns the computed value for this helper.
func installBlockForShell(shellName string) string {
	switch shellName {
	case "bash":
		return strings.TrimSpace(`
` + installStartMarker + `
case "$-" in
	*i*) ;;
	*) return ;;
esac

command_not_found_handle() {
  ash "$@"
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local args=("$@")
  local argc=${#args[@]}
	local cmd_lower
	cmd_lower="$(printf '%s' "$cmd" | tr '[:upper:]' '[:lower:]')"
	local natural_wrapper=0
	case "$cmd_lower" in
		what|which|who|where|at) natural_wrapper=1 ;;
	esac

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

	local has_path_like=0
	for a in "${args[@]}"; do
		if [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]]; then
			has_path_like=1
			break
		fi
	done
	if [[ $has_path_like -eq 1 && ( $natural_wrapper -eq 0 || $argc -eq 1 ) ]]; then
		return 1
	fi

	if [[ "$cmd_lower" == "at" ]]; then
		local first_at
		first_at="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
		first_at="${first_at%%[?!.,:;]}"
		if [[ "$first_at" =~ [0-9:] ]]; then
			return 1
		fi
		case "$first_at" in
			now|today|tomorrow|teatime|midnight|noon)
				return 1
				;;
			am|pm)
				return 1
				;;
		esac
	fi

  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[0]}" =~ ^[A-Za-z0-9_.-]+$ ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
			if [[ $argc -ge 2 ]]; then
				if [[ $has_path_like -eq 0 || ( $natural_wrapper -eq 1 && $argc -ge 3 ) ]]; then
					return 0
				fi
			fi
      ;;
  esac

	case "$cmd_lower" in
		what|which|who|where)
			if [[ $argc -ge 3 ]]; then
				local limit=4
				(( argc < limit )) && limit=$argc
				local i token raw
				for (( i=1; i<limit; i++ )); do
					raw="${args[$i]}"
					token="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
					token="${token%%[?!.,:;]}"
					case "$token" in
						is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who|if)
							return 0
							;;
					esac
				done
			fi
			;;
		say)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					out|something|a|an|the|please|why|how|when|where|who|what|can|could|should|would)
						return 0
						;;
				esac
			fi
			;;
		at)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[0]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					remind|tell|ask|message|note|please|what|when|how|why|who|where)
						return 0
						;;
				esac
			fi
			;;
	esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
say()   { _ash_route_or_delegate say   "$@"; }
Say()   { _ash_route_or_delegate Say   "$@"; }
at()    { _ash_route_or_delegate at    "$@"; }
At()    { _ash_route_or_delegate At    "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
` + installEndMarker)
	case "zsh":
		return strings.TrimSpace(`
` + installStartMarker + `
command_not_found_handler() {
  ash "$@"
  return $?
}

_ash_should_route() {
  local cmd="$1"
  shift
  local -a args
  args=("$@")
  local argc=${#args}
	local cmd_lower
	cmd_lower="$(printf '%s' "$cmd" | tr '[:upper:]' '[:lower:]')"
	local natural_wrapper=0
	case "$cmd_lower" in
		what|which|who|where|at) natural_wrapper=1 ;;
	esac

  [[ $argc -eq 0 ]] && return 1

  local a
  for a in "${args[@]}"; do
    [[ "$a" == -* ]] && return 1
  done

	local has_path_like=0
	for a in "${args[@]}"; do
		if [[ "$a" == */* || "$a" == ./* || "$a" == ../* ]]; then
			has_path_like=1
			break
		fi
	done
	if [[ $has_path_like -eq 1 && ( $natural_wrapper -eq 0 || $argc -eq 1 ) ]]; then
		return 1
	fi

	if [[ "$cmd_lower" == "at" ]]; then
		local first_at
		first_at="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
		first_at="${first_at%%[?!.,:;]}"
		if [[ "$first_at" =~ [0-9:] ]]; then
			return 1
		fi
		case "$first_at" in
			now|today|tomorrow|teatime|midnight|noon)
				return 1
				;;
			am|pm)
				return 1
				;;
		esac
	fi

  if [[ "$cmd" == "Time" || "$cmd" == "test" || "$cmd" == "Test" || "$cmd" == "type" || "$cmd" == "Type" ]]; then
    if [[ $argc -eq 1 && "${args[1]}" =~ '^[A-Za-z0-9_.-]+$' ]]; then
      return 1
    fi
  fi

  local full="$cmd"
  for a in "${args[@]}"; do
    full+=" $a"
  done

  [[ "$full" == *\? && $argc -ge 2 ]] && return 0

  local first
  first="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
  case "$first" in
    is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who)
			if [[ $argc -ge 2 ]]; then
				if [[ $has_path_like -eq 0 || ( $natural_wrapper -eq 1 && $argc -ge 3 ) ]]; then
					return 0
				fi
			fi
      ;;
  esac

	case "$cmd_lower" in
		what|which|who|where)
			if [[ $argc -ge 3 ]]; then
				local limit=4
				(( argc < limit )) && limit=$argc
				local i token raw
				for (( i=2; i<=limit; i++ )); do
					raw="${args[$i]}"
					token="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
					token="${token%%[?!.,:;]}"
					case "$token" in
						is|are|am|do|does|did|can|could|should|would|will|why|how|when|where|who|if)
							return 0
							;;
					esac
				done
			fi
			;;
		at)
			if [[ $argc -ge 2 ]]; then
				local first_token
				first_token="$(printf '%s' "${args[1]}" | tr '[:upper:]' '[:lower:]')"
				first_token="${first_token%%[?!.,:;]}"
				case "$first_token" in
					remind|tell|ask|message|note|please|what|when|how|why|who|where)
						return 0
						;;
				esac
			fi
			;;
	esac

  return 1
}

_ash_route_or_delegate() {
  local cmd="$1"
  shift
  if _ash_should_route "$cmd" "$@"; then
    ash "$cmd" "$@"
    return $?
  fi
  command "$cmd" "$@"
}

_ash_route_or_delegate_builtin() {
  local builtin_name="$1"
  shift
  if _ash_should_route "$builtin_name" "$@"; then
    ash "$builtin_name" "$@"
    return $?
  fi
  builtin "$builtin_name" "$@"
}

what()  { _ash_route_or_delegate what  "$@"; }
What()  { _ash_route_or_delegate What  "$@"; }
which() { _ash_route_or_delegate which "$@"; }
Which() { _ash_route_or_delegate Which "$@"; }
who()   { _ash_route_or_delegate who   "$@"; }
Who()   { _ash_route_or_delegate Who   "$@"; }
where() { _ash_route_or_delegate_builtin where "$@"; }
Where() { _ash_route_or_delegate_builtin where "$@"; }
at()    { _ash_route_or_delegate at    "$@"; }
At()    { _ash_route_or_delegate At    "$@"; }

test()  { _ash_route_or_delegate_builtin test "$@"; }
Test()  { _ash_route_or_delegate_builtin test "$@"; }
type()  { _ash_route_or_delegate_builtin type "$@"; }
Type()  { _ash_route_or_delegate_builtin type "$@"; }
Time()  { _ash_route_or_delegate Time "$@"; }
` + installEndMarker)
	default:
		return ""
	}
}

// runToolLoop runs the requested operation.
func runToolLoop(ctx context.Context, aiCfg aiConfig, userInput string, messages []message, shim mcpToolShim) (string, []message, error) {
	maxIters := maxToolIterations()
	tools := shim.ListTools()
	tasks := buildExecutionTasks(userInput, taskListMax())
	observations := make([]toolObservation, 0, 8)
	stallRounds := 0
	forcedToolRetryUsed := false
	debugLogf("Tool loop started: max_iters=%d tools=%d", maxIters, len(tools))

	for i := 0; i <= maxIters; i++ {
		debugLogf("Tool loop iteration=%d message_count=%d", i+1, len(messages))
		roundMessages := append([]message{}, messages...)
		stateMessage := buildExecutionStateMessage(userInput, tasks, observations, relevanceWindow())
		if len(roundMessages) == 0 {
			roundMessages = append(roundMessages, stateMessage)
		} else {
			insertAt := len(roundMessages) - 1
			roundMessages = append(roundMessages[:insertAt], append([]message{stateMessage}, roundMessages[insertAt:]...)...)
		}

		response, err := chat(ctx, aiCfg, roundMessages, tools)
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
			debugLogf("Assistant returned no tool calls")
			if hasPendingExecutionTasks(tasks) {
				stallRounds++
			} else {
				stallRounds = 0
			}

			if !forcedToolRetryUsed && shouldForceToolRetry(userInput, assistant.Content, tools) {
				forcedToolRetryUsed = true
				debugLogf("Execution-style prompt detected, forcing one retry with tool-use instruction")
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
			debugLogf("Tool invocation requested: name=%s args=%s", toolName, marshalForDebug(call.Function.Arguments))
			toolResult := shim.CallTool(ctx, toolName, call.Function.Arguments)
			debugLogf("Tool invocation result: name=%s result=%s", toolName, toolResult)
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
	if debugJSONLogging {
		record := map[string]any{
			"time":    timeNow().UTC().Format(time.RFC3339Nano),
			"level":   "debug",
			"message": fmt.Sprintf(format, args...),
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			_, _ = fmt.Fprintf(debugWriter, "[ash-debug] %s\n", sanitizeJSONError(err.Error()))
			return
		}
		_, _ = debugWriter.Write(append(encoded, '\n'))
		return
	}
	_, _ = fmt.Fprintf(debugWriter, "[ash-debug] "+format+"\n", args...)
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

// parseAIConfigFromEnv parses and validates input values.
func parseAIConfigFromEnv() (aiConfig, error) {
	if legacy := strings.TrimSpace(os.Getenv("AI")); legacy != "" {
		return aiConfig{}, errors.New("AI is no longer supported; use AI_ENDPOINT and AI_MODEL")
	}

	rawEndpoint := strings.TrimSpace(os.Getenv(aiEnvEndpoint))
	if rawEndpoint == "" {
		return aiConfig{}, fmt.Errorf("%s is required", aiEnvEndpoint)
	}

	model := strings.TrimSpace(os.Getenv(aiEnvModel))
	if model == "" {
		return aiConfig{}, fmt.Errorf("%s is required", aiEnvModel)
	}

	baseURL, host, scheme, err := parseAIEndpoint(rawEndpoint)
	if err != nil {
		return aiConfig{}, err
	}

	authType := strings.ToLower(strings.TrimSpace(os.Getenv(aiEnvAuthType)))
	authToken := strings.TrimSpace(os.Getenv(aiEnvAuthToken))
	if authToken != "" && authType == "" {
		return aiConfig{}, fmt.Errorf("%s is required when %s is set", aiEnvAuthType, aiEnvAuthToken)
	}
	if authType != "" && authType != "bearer" {
		return aiConfig{}, fmt.Errorf("%s must be bearer when set", aiEnvAuthType)
	}
	if authType == "bearer" && authToken == "" {
		return aiConfig{}, fmt.Errorf("%s is required when %s=bearer", aiEnvAuthToken, aiEnvAuthType)
	}

	if isCloudAIHost(host) {
		if scheme != "https" {
			return aiConfig{}, errors.New("AI_ENDPOINT must use https for cloud endpoints")
		}
		if authType != "bearer" || authToken == "" {
			return aiConfig{}, errors.New("cloud endpoints require AI_AUTH_TYPE=bearer and AI_AUTH_TOKEN")
		}
	}

	cfg := aiConfig{
		BaseURL:    baseURL,
		Model:      model,
		HistoryKey: fmt.Sprintf("%s/%s", baseURL, model),
	}
	if authType == "bearer" {
		cfg.Authorization = "Bearer " + authToken
	}

	return cfg, nil
}

// parseAIEndpoint parses and validates input values.
func parseAIEndpoint(value string) (baseURL string, host string, scheme string, err error) {
	u, err := url.Parse(value)
	if err != nil {
		return "", "", "", err
	}

	scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", "", "", errors.New("AI_ENDPOINT scheme must be http or https")
	}
	host = strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", "", "", errors.New("AI_ENDPOINT host is required")
	}
	if strings.TrimSpace(u.RawQuery) != "" || strings.TrimSpace(u.Fragment) != "" {
		return "", "", "", errors.New("AI_ENDPOINT must not include query or fragment")
	}

	cleanPath := strings.TrimRight(strings.TrimSpace(u.Path), "/")
	baseURL = fmt.Sprintf("%s://%s%s", scheme, u.Host, cleanPath)
	return baseURL, host, scheme, nil
}

// isCloudAIHost reports whether the condition is true.
func isCloudAIHost(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "localhost" {
		return false
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback()
}

// readSystemPrompt reads data from the filesystem.
func readSystemPrompt() (string, error) {
	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, systemFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return expandSystemPrompt(string(content)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}

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

// loadAllowlistedCommands loads data from storage.
func loadAllowlistedCommands() (map[string]struct{}, error) {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_ALLOWLIST")); raw != "" {
		return parseAllowlistCSV(raw), nil
	}

	if root, err := ashWorkspaceDir(); err == nil {
		canonicalPath := filepath.Join(root, toolsFileName)
		if content, err := osReadFile(canonicalPath); err == nil {
			return parseAllowlistFile(string(content)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
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

// parseAllowlistCSV parses and validates input values.
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

// parseAllowlistFile parses and validates input values.
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

// normalizeToolName normalizes and returns the canonical value.
func normalizeToolName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}

// expandSystemPrompt returns the computed value for this helper.
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

// historyLimit returns the computed value for this helper.
func historyLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_HISTORY_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultHistoryMax
}

// aiTimeout returns the computed value for this helper.
func aiTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("AI_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}

	return defaultAITimeout
}

// toolTimeout returns the computed value for this helper.
func toolTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolTimeout
}

// toolOutputLimit returns the computed value for this helper.
func toolOutputLimit() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TOOL_OUTPUT_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultToolOutputMax
}

// maxToolIterations returns the computed value for this helper.
func maxToolIterations() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_MAX_TOOL_ITERS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return defaultMaxToolIters
}

// keepRecentMessages returns the computed value for this helper.
func keepRecentMessages(messages []message, max int) []message {
	if len(messages) <= max {
		return messages
	}

	return append([]message(nil), messages[len(messages)-max:]...)
}

// taskListMax returns the computed value for this helper.
func taskListMax() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TASK_MAX")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultTaskMax
}

// relevanceWindow returns the computed value for this helper.
func relevanceWindow() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_RELEVANCE_WINDOW")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultRelevanceWin
}

// maxTaskStallRounds returns the computed value for this helper.
func maxTaskStallRounds() int {
	if raw := strings.TrimSpace(os.Getenv("ASH_TASK_STALL_ROUNDS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultStallRounds
}

// buildExecutionTasks builds and returns a derived value.
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

// buildExecutionStateMessage builds and returns a derived value.
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

// parseToolObservation parses and validates input values.
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

// promoteNextPendingTask returns the computed value for this helper.
func promoteNextPendingTask(tasks []executionTask) {
	for i := range tasks {
		if tasks[i].Status == taskStatusPending {
			tasks[i].Status = taskStatusRunning
			return
		}
	}
}

// applyToolObservationToTasks returns the computed value for this helper.
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

// hasPendingExecutionTasks reports whether the condition is true.
func hasPendingExecutionTasks(tasks []executionTask) bool {
	for _, task := range tasks {
		if task.Status == taskStatusPending || task.Status == taskStatusRunning {
			return true
		}
	}
	return false
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

// ListTools returns the tool definitions exposed by the local shim.
func (s localToolShim) ListTools() []toolDefinition {
	runUnixDescription := "Run a single allowlisted Unix executable with direct args and no shell expansion"
	allowed := sortedAllowlist(s.allowlist)
	if len(allowed) > 0 {
		runUnixDescription += ". Allowlisted executables: " + strings.Join(allowed, ", ")
	}

	return []toolDefinition{
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

// CallTool dispatches a tool call to the matching local shim handler.
func (s localToolShim) CallTool(ctx context.Context, name string, args map[string]any) string {
	var result toolCommandResult

	switch name {
	case "run_unix_command":
		result = s.callUnixCommand(ctx, args)
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
	default:
		result = toolCommandResult{OK: false, Error: fmt.Sprintf("unknown tool: %s", name)}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":"failed to encode tool result: %s"}`, sanitizeJSONError(err.Error()))
	}

	return string(encoded)
}

// callUnixCommand invokes the underlying tool implementation.
func (s localToolShim) callUnixCommand(ctx context.Context, args map[string]any) toolCommandResult {
	commandInput, ok := toStringArg(args["command"])
	if !ok {
		return toolCommandResult{OK: false, Error: "command must be a string"}
	}

	fields := strings.Fields(commandInput)
	if len(fields) == 0 {
		return toolCommandResult{OK: false, Error: "command must be a bare executable name"}
	}

	commandName := fields[0]
	inlineArgs := fields[1:]

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
	argv = append(inlineArgs, argv...)

	for _, arg := range argv {
		if isBlockedArgument(arg) {
			return toolCommandResult{OK: false, Command: commandName, Error: "argument contains blocked shell control pattern"}
		}
	}

	return toolCommandRunner(ctx, commandName, argv, toolTimeout(), toolOutputLimit())
}

// callPython3 invokes the underlying tool implementation.
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

// callScheduleFuturePrompt invokes the underlying tool implementation.
func (s localToolShim) callScheduleFuturePrompt(ctx context.Context, args map[string]any) toolCommandResult {
	prompt, ok := toStringArg(args["prompt"])
	if !ok || strings.TrimSpace(prompt) == "" {
		return toolCommandResult{OK: false, Command: "launchctl", Error: "prompt must be a non-empty string"}
	}
	when, ok := toStringArg(args["when"])
	if !ok || strings.TrimSpace(when) == "" {
		return toolCommandResult{OK: false, Command: "launchctl", Error: "when must be a non-empty string"}
	}
	scheduledAt, err := parseFutureScheduleTime(when, timeNow())
	if err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error()}
	}

	cwd, err := optionalStringArg(args, "cwd")
	if err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error()}
	}

	label, plistPath, plistContent, err := buildFuturePromptLaunchAgent(prompt, cwd, scheduledAt)
	if err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error()}
	}
	if err := osMkdirAll(filepath.Dir(plistPath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error()}
	}
	if err := osWriteFile(plistPath, []byte(plistContent), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "launchctl", Error: err.Error()}
	}

	serviceDomain := fmt.Sprintf("gui/%d", os.Getuid())
	result := toolCommandRunner(ctx, "launchctl", []string{"bootstrap", serviceDomain, plistPath}, defaultLaunchdTimeout, toolOutputLimit())
	if !result.OK {
		return result
	}

	result.Stdout = fmt.Sprintf("scheduled future job label=%s at=%s plist=%s", label, scheduledAt.Format(time.RFC3339), plistPath)
	return result
}

// callScheduleRecurringPrompt invokes the underlying tool implementation.
func (s localToolShim) callScheduleRecurringPrompt(ctx context.Context, args map[string]any) toolCommandResult {
	prompt, ok := toStringArg(args["prompt"])
	if !ok || strings.TrimSpace(prompt) == "" {
		return toolCommandResult{OK: false, Command: "crontab", Error: "prompt must be a non-empty string"}
	}

	cronExpr, ok := toStringArg(args["cron"])
	if !ok {
		return toolCommandResult{OK: false, Command: "crontab", Error: "cron must be a string"}
	}
	cronExpr = strings.TrimSpace(cronExpr)
	if err := validateCronExpr(cronExpr); err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
	}

	cwd, err := optionalStringArg(args, "cwd")
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
	}
	purpose, err := optionalStringArg(args, "purpose")
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
	}

	meta, line, err := buildRecurringJobLine(prompt, cronExpr, cwd, purpose, "")
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
	}

	content, err := loadCurrentCrontab(ctx)
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab -l", Error: err.Error()}
	}
	updated := appendCrontabLine(content, line)
	writeResult := writeCrontab(ctx, updated)
	if !writeResult.OK {
		return writeResult
	}
	writeResult.Stdout = strings.TrimSpace(fmt.Sprintf("scheduled recurring job id=%s cron=%s", meta.ID, meta.Cron))
	return writeResult
}

// callManageRecurringJobs invokes the underlying tool implementation.
func (s localToolShim) callManageRecurringJobs(ctx context.Context, args map[string]any) toolCommandResult {
	actionRaw, ok := toStringArg(args["action"])
	if !ok {
		return toolCommandResult{OK: false, Command: "crontab", Error: "action must be a string"}
	}
	action := strings.ToLower(strings.TrimSpace(actionRaw))

	content, err := loadCurrentCrontab(ctx)
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab -l", Error: err.Error()}
	}
	records, err := parseRecurringJobs(content)
	if err != nil {
		return toolCommandResult{OK: false, Command: "crontab -l", Error: err.Error()}
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
					b.WriteString(fmt.Sprintf("id=%s cron=%s cwd=%s purpose=%s\n", rec.Meta.ID, rec.Meta.Cron, rec.Meta.Cwd, strings.TrimSpace(rec.Meta.Purpose)))
				}
			}
			return toolCommandResult{OK: true, Command: "crontab -l", ExitCode: 0, Stdout: strings.TrimSpace(b.String())}
		}
		rec, found := findRecurringJob(records, id)
		if !found {
			return toolCommandResult{OK: false, Command: "crontab -l", Error: "recurring job id not found"}
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
			return toolCommandResult{OK: false, Command: "crontab", Error: "id is required for cancel"}
		}
		updated, removed := removeRecurringJobFromCrontab(content, id)
		if !removed {
			return toolCommandResult{OK: false, Command: "crontab", Error: "recurring job id not found"}
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
			return toolCommandResult{OK: false, Command: "crontab", Error: "id is required for modify"}
		}
		rec, found := findRecurringJob(records, id)
		if !found {
			return toolCommandResult{OK: false, Command: "crontab", Error: "recurring job id not found"}
		}

		if cronExpr, ok := args["cron"]; ok {
			value, ok := cronExpr.(string)
			if !ok {
				return toolCommandResult{OK: false, Command: "crontab", Error: "cron must be a string"}
			}
			value = strings.TrimSpace(value)
			if err := validateCronExpr(value); err != nil {
				return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
			}
			rec.Meta.Cron = value
		}
		if prompt, ok := args["prompt"]; ok {
			value, ok := prompt.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return toolCommandResult{OK: false, Command: "crontab", Error: "prompt must be a non-empty string"}
			}
			rec.Meta.Prompt = strings.TrimSpace(value)
		}
		if cwd, ok := args["cwd"]; ok {
			value, ok := cwd.(string)
			if !ok {
				return toolCommandResult{OK: false, Command: "crontab", Error: "cwd must be a string"}
			}
			rec.Meta.Cwd = strings.TrimSpace(value)
		}
		if purpose, ok := args["purpose"]; ok {
			value, ok := purpose.(string)
			if !ok {
				return toolCommandResult{OK: false, Command: "crontab", Error: "purpose must be a string"}
			}
			rec.Meta.Purpose = strings.TrimSpace(value)
		}

		script, err := buildScheduledInvocationScriptWithEnv(rec.Meta.Prompt, rec.Meta.Cwd, rec.Meta.Env)
		if err != nil {
			return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
		}
		line, err := buildRecurringCrontabLine(rec.Meta, script)
		if err != nil {
			return toolCommandResult{OK: false, Command: "crontab", Error: err.Error()}
		}
		updated := replaceRecurringJobLine(content, id, line)
		result := writeCrontab(ctx, updated)
		if !result.OK {
			return result
		}
		result.Stdout = fmt.Sprintf("modified recurring job id=%s", id)
		return result
	default:
		return toolCommandResult{OK: false, Command: "crontab", Error: "action must be one of list, cancel, modify, explain"}
	}
}

// callReadWorkspaceFile invokes the underlying tool implementation.
func (s localToolShim) callReadWorkspaceFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: "path must be a non-empty string"}
	}
	root, err := ashWorkspaceDir()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: err.Error()}
	}
	absolutePath, relPath, err := resolveWorkspacePath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: err.Error()}
	}

	content, err := osReadFile(absolutePath)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_read_workspace_file", Error: err.Error()}
	}

	return toolCommandResult{OK: true, Command: "ash_read_workspace_file", ExitCode: 0, Stdout: fmt.Sprintf("path=%s\n%s", relPath, string(content))}
}

// callWriteWorkspaceFile invokes the underlying tool implementation.
func (s localToolShim) callWriteWorkspaceFile(args map[string]any) toolCommandResult {
	rel, ok := toStringArg(args["path"])
	if !ok || strings.TrimSpace(rel) == "" {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: "path must be a non-empty string"}
	}
	content, ok := toStringArg(args["content"])
	if !ok {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: "content must be a string"}
	}
	purpose, ok := toStringArg(args["purpose"])
	if !ok || strings.TrimSpace(purpose) == "" {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: "purpose must be a non-empty string"}
	}

	root, err := ashWorkspaceDir()
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error()}
	}
	if err := osMkdirAll(root, 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error()}
	}
	absolutePath, relPath, err := resolveWorkspacePath(root, rel)
	if err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error()}
	}

	if err := osMkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error()}
	}
	if err := osWriteFile(absolutePath, []byte(content), 0o600); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error()}
	}
	if err := updateWorkspaceInventory(root, relPath, strings.TrimSpace(purpose)); err != nil {
		return toolCommandResult{OK: false, Command: "ash_write_workspace_file", Error: err.Error()}
	}

	return toolCommandResult{OK: true, Command: "ash_write_workspace_file", ExitCode: 0, Stdout: fmt.Sprintf("wrote %s", relPath)}
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
