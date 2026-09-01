package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAttachFlags(t *testing.T) {
	remaining, paths, err := parseAttachFlags([]string{"hello", "--attach", "/tmp/a.png", "world", "--attach", "/tmp/b.pdf"})
	if err != nil {
		t.Fatalf("parseAttachFlags returned error: %v", err)
	}
	if !reflectStringsEqual(remaining, []string{"hello", "world"}) {
		t.Fatalf("unexpected remaining args: %+v", remaining)
	}
	if !reflectStringsEqual(paths, []string{"/tmp/a.png", "/tmp/b.pdf"}) {
		t.Fatalf("unexpected attach paths: %+v", paths)
	}
}

func TestParseAttachFlagsMissingValue(t *testing.T) {
	_, _, err := parseAttachFlags([]string{"--attach"})
	if err == nil {
		t.Fatal("expected error for --attach with no value")
	}
}

func TestLoadAttachmentSniffsMimeAndReadsData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	att, err := loadAttachment(path)
	if err != nil {
		t.Fatalf("loadAttachment returned error: %v", err)
	}
	if att.FileName != "note.txt" {
		t.Fatalf("unexpected file name: got %q", att.FileName)
	}
	if !strings.HasPrefix(att.MimeType, "text/plain") {
		t.Fatalf("unexpected mime type: got %q", att.MimeType)
	}
	if string(att.Data) != "hello world" {
		t.Fatalf("unexpected data: got %q", att.Data)
	}
}

func TestLoadAttachmentEnforcesSizeLimit(t *testing.T) {
	t.Setenv(ashEnvAttachmentMaxBytes, "4")
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, []byte("too big"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if _, err := loadAttachment(path); err == nil {
		t.Fatal("expected size-limit error")
	}
}

func TestLoadAttachmentRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadAttachment(dir); err == nil {
		t.Fatal("expected error for directory path")
	}
}

func TestWriteResponseAttachmentsWritesFiles(t *testing.T) {
	dir := t.TempDir()
	written, err := writeResponseAttachments(dir, []attachment{
		{FileName: "out.txt", Data: []byte("payload"), MimeType: "text/plain"},
	})
	if err != nil {
		t.Fatalf("writeResponseAttachments returned error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected one written path, got %d", len(written))
	}
	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("failed to read written attachment: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("unexpected written content: got %q", data)
	}
}

func TestChatOpenAIResponsesAdapterEncodesAttachments(t *testing.T) {
	originalIsRealOpenAIHost := isRealOpenAIHost
	isRealOpenAIHost = func(string) bool { return true }
	t.Cleanup(func() { isRealOpenAIHost = originalIsRealOpenAIHost })

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`))
	}))
	defer srv.Close()

	cfg := aiConfig{
		BaseURL:       srv.URL + "/v1",
		Model:         "gpt-4.1-mini",
		Authorization: "Bearer test-key",
		AuthToken:     "test-key",
		Provider:      providerOpenAI,
	}
	msgs := []message{{
		Role:    "user",
		Content: "what's in this image?",
		Attachments: []attachment{
			{MimeType: "image/png", FileName: "pic.png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
		},
	}}
	if _, err := chat(context.Background(), cfg, msgs, nil); err != nil {
		t.Fatalf("chat returned error: %v", err)
	}

	input, ok := gotBody["input"].([]any)
	if !ok || len(input) == 0 {
		t.Fatalf("expected input array in request body, got %+v", gotBody)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal input for inspection: %v", err)
	}
	if !strings.Contains(string(raw), "input_image") {
		t.Fatalf("expected request to contain an input_image content part, got %s", raw)
	}
}

func reflectStringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
