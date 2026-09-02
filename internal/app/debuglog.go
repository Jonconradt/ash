package app

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func newStructuredLogger(w io.Writer, level slog.Level) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.MessageKey {
				attr.Key = "message"
			}
			if attr.Key == slog.LevelKey {
				attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
			}
			return attr
		},
	}))
}

// configureDebugLogging wires debug logging to stderr or a rotating log file based on the current environment.
func configureDebugLogging(writers ...io.Writer) {
	defaultWriter := debugWriter
	if len(writers) > 0 && writers[0] != nil {
		defaultWriter = writers[0]
	}
	if defaultWriter == nil {
		defaultWriter = os.Stderr
	}
	currentWriter := defaultWriter

	if verboseLoggingEnabled() {
		logFile := strings.TrimSpace(os.Getenv("ASH_LOG_FILE"))
		if logFile != "" {
			maxBytes := defaultSchedulerLogMaxBytes
			if raw := strings.TrimSpace(os.Getenv("ASH_LOG_MAX_BYTES")); raw != "" {
				if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
					maxBytes = parsed
				}
			}

			writer, err := newRotatingSchedulerLogWriter(logFile, maxBytes)
			if err == nil {
				currentWriter = writer
			}
		}
	}

	level := slog.LevelInfo
	if verboseLoggingEnabled() {
		level = slog.LevelDebug
	}

	appLogger = newStructuredLogger(currentWriter, level)
	debugWriter = currentWriter
	debugJSONLogging = true
	slog.SetDefault(appLogger)
}

type rotatingSchedulerLogWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

// newRotatingSchedulerLogWriter creates a log writer that rotates the log file once it exceeds the maximum size.
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
