package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type agentBudget struct {
	mu    sync.Mutex
	limit int
	used  int
}

func newAgentBudget(limit int) *agentBudget {
	if limit <= 0 {
		limit = defaultMaxAgents
	}
	return &agentBudget{limit: limit}
}

func (b *agentBudget) reserve() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used >= b.limit {
		return false
	}
	b.used++
	return true
}

func (b *agentBudget) release() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used > 0 {
		b.used--
	}
}

func isChildAgent() bool {
	return strings.TrimSpace(os.Getenv(childAgentEnvName)) == childAgentEnvValue
}

func hashForLog(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func isAshExecutableName(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	return base == "ash" || base == "ash.exe"
}

func pipelineContainsAsh(args map[string]any) bool {
	pipeline, ok := toStringArg(args["pipeline"])
	if !ok {
		return false
	}
	for _, part := range strings.Split(pipeline, "|") {
		fields := strings.Fields(part)
		if len(fields) > 0 && isAshExecutableName(fields[0]) {
			return true
		}
	}
	return false
}

func withEnvironmentValues(values map[string]string) []string {
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replace := keys[key]; replace {
				continue
			}
		}
		env = append(env, entry)
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

const childSessionAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func generateChildSessionID(parentID string) (string, error) {
	parentID = strings.TrimSpace(parentID)
	if !validSessionID(parentID) {
		return "", errors.New("parent SESSION_ID is invalid")
	}
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, value := range raw {
		raw[i] = childSessionAlphabet[int(value)%len(childSessionAlphabet)]
	}
	return parentID + "." + string(raw), nil
}

func validSessionID(value string) bool {
	return len(value) <= maxSessionIDLength && sessionIDPattern.MatchString(value)
}

func runSubAgentCommand(ctx context.Context, prompt, childID string) toolCommandResult {
	ashPath, err := osExecutable()
	if err != nil {
		return toolCommandResult{OK: false, Command: "run_sub_agent", Error: "executable lookup failed", EID: "J9QJ8y8p"}
	}
	commandCtx, cancel := context.WithTimeout(ctx, aiTimeout())
	defer cancel()
	cmd := execCommandContext(commandCtx, ashPath, prompt)
	configureProcessGroup(cmd)
	cmd.Env = withEnvironmentValues(map[string]string{
		sessionIDEnvName:  childID,
		childAgentEnvName: childAgentEnvValue,
		"ASH_VERBOSE":     "0",
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return toolCommandResult{OK: false, Command: "run_sub_agent", ExitCode: -1, Error: err.Error(), EID: "J9QJ8y8p"}
	}
	processDone := make(chan struct{})
	go func() {
		select {
		case <-commandCtx.Done():
			terminateProcessTree(cmd)
		case <-processDone:
		}
	}()
	err = cmd.Wait()
	close(processDone)
	result := toolCommandResult{
		OK:      err == nil,
		Command: "run_sub_agent",
		Stdout:  truncateForToolOutput(stdout.String(), toolOutputLimit()),
		Stderr:  truncateForToolOutput(stderr.String(), toolOutputLimit()),
	}
	if err == nil {
		result.ExitCode = 0
		return result
	}
	if errors.Is(commandCtx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		result.ExitCode = -1
		result.Error = "sub-agent canceled"
		return result
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		result.Error = fmt.Sprintf("sub-agent timed out after %s", aiTimeout())
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		result.Error = fmt.Sprintf("sub-agent exited with status %d", result.ExitCode)
		return result
	}
	result.ExitCode = -1
	result.Error = err.Error()
	return result
}

// runToolPipeline executes commands without a shell, connecting each stdout to the next stdin.
func runToolPipeline(ctx context.Context, commands [][]string, display string, timeout time.Duration, outputMax int) toolCommandResult {
	if len(commands) < 2 {
		return toolCommandResult{OK: false, Command: display, ExitCode: -1, Error: "pipeline commands must not be empty", EID: "8Q8QmB9t"}
	}
	if len(commands) > 16 {
		return toolCommandResult{OK: false, Command: display, ExitCode: -1, Error: "pipeline cannot contain more than 16 commands", EID: "8Q8QmB9t"}
	}

	sanitized := make([][]string, len(commands))
	for i, command := range commands {
		var err error
		sanitized[i], err = sanitizeCommandArgs(command)
		if err != nil {
			return toolCommandResult{OK: false, Command: display, ExitCode: -1, Error: err.Error(), EID: "nnbIek1C"}
		}
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pipes := make([]struct{ reader, writer *os.File }, len(sanitized)-1)
	for i := range pipes {
		reader, writer, err := os.Pipe()
		if err != nil {
			return toolCommandResult{OK: false, Command: display, ExitCode: -1, Error: err.Error(), EID: "qQ4x4j9M"}
		}
		pipes[i] = struct{ reader, writer *os.File }{reader: reader, writer: writer}
	}
	// Fallback safety net only; the happy path closes every fd explicitly below
	// once both connected processes have started (see comment near Start()).
	defer func() {
		for _, pipe := range pipes {
			_ = pipe.reader.Close()
			_ = pipe.writer.Close()
		}
	}()

	processes := make([]*exec.Cmd, len(sanitized))
	for i, command := range sanitized {
		// #nosec G204 -- command has passed sanitizeCommandArgs and executes without a shell.
		process := exec.CommandContext(commandCtx, command[0], command[1:]...)
		if i > 0 {
			process.Stdin = pipes[i-1].reader
		}
		if i < len(pipes) {
			process.Stdout = pipes[i].writer
		}
		processes[i] = process
	}
	var consumerOutput bytes.Buffer
	processes[len(processes)-1].Stdout = &consumerOutput
	started := make([]*exec.Cmd, 0, len(processes))
	for _, process := range processes {
		if err := process.Start(); err != nil {
			for _, startedProcess := range started {
				_ = startedProcess.Process.Kill()
			}
			for _, startedProcess := range started {
				_ = startedProcess.Wait()
			}
			return toolCommandResult{OK: false, Command: display, ExitCode: -1, Error: err.Error(), EID: "j7qQm8vN"}
		}
		started = append(started, process)
	}
	// exec duplicates each pipe fd into the connected children; the parent's own
	// copies must be closed now or the reading end of every pipe never sees EOF
	// (downstream stages that read to EOF, e.g. python3/grep/cat, hang until killed).
	for _, pipe := range pipes {
		_ = pipe.reader.Close()
		_ = pipe.writer.Close()
	}

	var waitErr error
	for _, process := range processes {
		if err := process.Wait(); err != nil && waitErr == nil {
			waitErr = err
		}
	}
	if waitErr != nil {
		return toolCommandResult{OK: false, Command: display, ExitCode: pipelineExitCode(waitErr), Stderr: truncateForToolOutput(waitErr.Error(), outputMax), Error: waitErr.Error(), EID: "j7qQm8vN"}
	}

	return toolCommandResult{OK: true, Command: display, ExitCode: 0, Stdout: truncateForToolOutput(consumerOutput.String(), outputMax)}
}

func sanitizeCommandArgs(command []string) ([]string, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, errors.New("pipeline command must have a non-empty executable")
	}

	sanitized := append([]string(nil), command...)
	for _, arg := range sanitized {
		if isBlockedArgument(arg) {
			return nil, errors.New("argument contains blocked shell control pattern")
		}
	}
	return sanitized, nil
}

func pipelineExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// isInteractiveStdin reports whether stdin is connected to a terminal device.
func isInteractiveStdin() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// readAllPromptFromStdin reads all available stdin data as prompt text.
func readAllPromptFromStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// runToolCommandWithInput executes a command with optional stdin and returns its captured result.
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

// runToolCommand executes a command and returns its captured result, including output and any error details.
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

// truncateForToolOutput truncates tool output to the configured maximum length while preserving the tail of the content.
func truncateForToolOutput(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "\n...truncated..."
}

// toStringArg returns the supplied value as a string when it is already a string.
func toStringArg(value any) (string, bool) {
	v, ok := value.(string)
	if !ok {
		return "", false
	}
	return v, true
}

// toStringSliceArg converts a tool argument value into a slice of strings when it is an array of strings.
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
