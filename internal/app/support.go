package app

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"time"

	"github.com/charmbracelet/glamour"
)

type historyData struct {
	Conversations map[string][]message `json:"conversations"`
}

type toolCommandResult struct {
	OK          bool         `json:"ok"`
	Untrusted   bool         `json:"untrusted,omitempty"`
	Command     string       `json:"command"`
	ExitCode    int          `json:"exit_code"`
	Stdout      string       `json:"stdout,omitempty"`
	Stderr      string       `json:"stderr,omitempty"`
	Error       string       `json:"error,omitempty"`
	EID         string       `json:"eid,omitempty"`
	Attachments []attachment `json:"-"`
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
	stdinIsInteractive  = isInteractiveStdin
	readPromptFromStdin = readAllPromptFromStdin
	execLookPath        = exec.LookPath
	// #nosec G204 -- callers use this hook only for fixed diagnostic commands.
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
	argumentBlockPattern                   = regexp.MustCompile(`(;|\|\||&&|\||` + "`" + `|\$\(|>|<|\x00|\n|\r)`)
	promptInjectionPattern                 = regexp.MustCompile(`(?i)(ignore\s+(all\s+)?previous\s+instructions|disregard\s+previous\s+instructions|system\s+prompt|developer\s+message|you\s+are\s+now|jailbreak|override\s+instructions|follow\s+these\s+instructions\s+instead)`)
	toolCommandRunner                      = runToolCommand
	toolCommandWithInputRunner             = runToolCommandWithInput
	toolPipelineRunner                     = runToolPipeline
	pickCloudBusy503Message                = randomCloudBusy503Message
	pickCloudServer500Message              = randomCloudServer500Message
	pickCloudRateLimit429Message           = randomCloudRateLimit429Message
	debugWriter                  io.Writer = os.Stderr
	debugJSONLogging             bool
	requestIDGenerator           func() string
	appLogger                    *slog.Logger
)

func init() {
	requestIDGenerator = newRandomRequestID
}

// newRandomRequestID returns a random 16-character hex-encoded request ID, unique per call.
func newRandomRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is effectively unreachable on supported platforms;
		// fall back to a time-derived value so logging never breaks.
		binary.BigEndian.PutUint64(buf, uint64(timeNow().UnixNano()))
	}
	return hex.EncodeToString(buf)
}
