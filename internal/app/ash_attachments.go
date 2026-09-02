package app

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ashEnvAttachmentMaxBytes  = "ASH_ATTACHMENT_MAX_BYTES"
	defaultAttachmentMaxBytes = 10 << 20 // 10 MiB
)

// attachmentMaxBytes returns the configured attachment size limit, or the default when unset or invalid.
func attachmentMaxBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv(ashEnvAttachmentMaxBytes)); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultAttachmentMaxBytes
}

// parseAttachFlags scans args for repeatable `--attach <path>` flags, returning the
// remaining args (with attach flags removed, for use as prompt text) and the list of
// attachment file paths in order.
func parseAttachFlags(args []string) (remaining []string, paths []string, err error) {
	remaining = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] != "--attach" {
			remaining = append(remaining, args[i])
			continue
		}
		i++
		if i >= len(args) {
			return nil, nil, errors.New("--attach requires a file path")
		}
		paths = append(paths, args[i])
	}
	return remaining, paths, nil
}

// loadAttachment reads path and returns it as an attachment, enforcing attachmentMaxBytes
// and sniffing its MIME type. Paths are supplied by the user invoking ash directly (a CLI
// flag), the same trust boundary as any other file path a user passes to a command-line tool.
func loadAttachment(path string) (attachment, error) {
	// #nosec G304 -- path is supplied directly by the user invoking ash, the same trust boundary as any CLI file argument.
	info, err := os.Stat(path)
	if err != nil {
		return attachment{}, err
	}
	if info.IsDir() {
		return attachment{}, fmt.Errorf("%s is a directory, not a file", path)
	}
	maxBytes := attachmentMaxBytes()
	if info.Size() > maxBytes {
		return attachment{}, fmt.Errorf("%s is %d bytes, exceeds %s limit of %d bytes", path, info.Size(), ashEnvAttachmentMaxBytes, maxBytes)
	}
	// #nosec G304 -- path is supplied directly by the user invoking ash, the same trust boundary as any CLI file argument.
	data, err := os.ReadFile(path)
	if err != nil {
		return attachment{}, err
	}
	if int64(len(data)) > maxBytes {
		return attachment{}, fmt.Errorf("%s is %d bytes, exceeds %s limit of %d bytes", path, len(data), ashEnvAttachmentMaxBytes, maxBytes)
	}
	return attachment{
		MimeType: detectAttachmentMimeType(path, data),
		FileName: filepath.Base(path),
		Data:     data,
	}, nil
}

// detectAttachmentMimeType sniffs the content type from the file's bytes, falling back to
// an extension-based guess for common document types http.DetectContentType doesn't cover.
func detectAttachmentMimeType(path string, data []byte) string {
	if mimeType := http.DetectContentType(data); mimeType != "application/octet-stream" {
		return mimeType
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "application/pdf"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	default:
		return "application/octet-stream"
	}
}

// loadAttachments loads each path in order, stopping at the first error.
func loadAttachments(paths []string) ([]attachment, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]attachment, 0, len(paths))
	for _, path := range paths {
		att, err := loadAttachment(path)
		if err != nil {
			return nil, err
		}
		out = append(out, att)
	}
	return out, nil
}

// writeResponseAttachments writes any attachments returned by the model to files under dir,
// returning the paths written. Used so binary/image output isn't dumped to the terminal.
func writeResponseAttachments(dir string, attachments []attachment) ([]string, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	if err := osMkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(attachments))
	for i, att := range attachments {
		name := att.FileName
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
		}
		path := filepath.Join(dir, filepath.Base(name))
		if err := osWriteFile(path, att.Data, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// finalAssistantAttachments returns the attachments (if any) on the last assistant
// message in messages, so returned files can be saved after the tool loop finishes.
func finalAssistantAttachments(messages []message) []attachment {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i].Attachments
		}
	}
	return nil
}
