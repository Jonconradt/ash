package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGetHistoryPath(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("SESSION_ID", "abc123")
	t.Setenv("ASH_SCHEDULED_TASK", "")

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}

	want := filepath.Join(home, ashWorkspaceDirName, historyDirName, "abc123.json")
	if path != want {
		t.Fatalf("path mismatch: got %q want %q", path, want)
	}
}

func TestGetHistoryPathScheduled(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("SESSION_ID", "abc123")
	t.Setenv("ASH_SCHEDULED_TASK", "1")

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}

	want := filepath.Join(home, ashWorkspaceDirName, historyDirName, "task_abc123.json")
	if path != want {
		t.Fatalf("path mismatch: got %q want %q", path, want)
	}
}

func TestGetHistoryPathPreservesChildSessionID(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("SESSION_ID", "parent.abc123")
	t.Setenv("ASH_SCHEDULED_TASK", "")

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}
	want := filepath.Join(home, ashWorkspaceDirName, historyDirName, "parent.abc123.json")
	if path != want {
		t.Fatalf("path mismatch: got %q want %q", path, want)
	}
}

func TestGetHistoryPathError(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	osUserHomeDir = func() (string, error) { return "", errors.New("no home") }
	_, err := getHistoryPath()
	if err == nil || !strings.Contains(err.Error(), "no home") {
		t.Fatalf("expected no home error, got %v", err)
	}
}

func TestAITimeout(t *testing.T) {
	t.Run("configured duration", func(t *testing.T) {
		t.Setenv("AI_TIMEOUT", "45s")
		if got := aiTimeout(); got != 45*time.Second {
			t.Fatalf("aiTimeout mismatch: got %s want %s", got, 45*time.Second)
		}
	})

	t.Run("invalid falls back", func(t *testing.T) {
		t.Setenv("AI_TIMEOUT", "not-a-duration")
		if got := aiTimeout(); got != defaultAITimeout {
			t.Fatalf("aiTimeout fallback mismatch: got %s want %s", got, defaultAITimeout)
		}
	})
}

func TestLoadHistoryNotFound(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	path := filepath.Join(t.TempDir(), "missing.json")
	osReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	data, err := loadHistory(path)
	if err != nil {
		t.Fatalf("loadHistory returned error: %v", err)
	}
	if data.Conversations == nil {
		t.Fatalf("expected initialized conversations map")
	}
	if len(data.Conversations) != 0 {
		t.Fatalf("expected empty conversations map, got %d entries", len(data.Conversations))
	}
}

func TestLoadHistoryReadError(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	osReadFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
	_, err := loadHistory("ignored")
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected read failed error, got %v", err)
	}
}

func TestLoadHistoryUnmarshalError(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	osReadFile = func(string) ([]byte, error) { return []byte("not-json"), nil }
	_, err := loadHistory("ignored")
	if err == nil {
		t.Fatalf("expected unmarshal error")
	}
}

func TestLoadHistoryInitializesNilMap(t *testing.T) {
	origReadFile := osReadFile
	t.Cleanup(func() { osReadFile = origReadFile })

	osReadFile = func(string) ([]byte, error) { return []byte(`{"conversations":null}`), nil }
	data, err := loadHistory("ignored")
	if err != nil {
		t.Fatalf("loadHistory returned error: %v", err)
	}
	if data.Conversations == nil {
		t.Fatalf("expected non-nil conversations map")
	}
}

func TestSaveAndLoadHistoryRoundTrip(t *testing.T) {
	origReadFile := osReadFile
	origWriteFile := osWriteFile
	t.Cleanup(func() {
		osReadFile = origReadFile
		osWriteFile = origWriteFile
	})

	path := filepath.Join(t.TempDir(), "history.json")
	input := historyData{
		Conversations: map[string][]message{
			"k": {
				{Role: "user", Content: "u"},
				{Role: "assistant", Content: "a"},
			},
		},
	}

	if err := saveHistory(path, input); err != nil {
		t.Fatalf("saveHistory returned error: %v", err)
	}

	output, err := loadHistory(path)
	if err != nil {
		t.Fatalf("loadHistory returned error: %v", err)
	}

	if !reflect.DeepEqual(input, output) {
		inJSON, _ := json.Marshal(input)
		outJSON, _ := json.Marshal(output)
		t.Fatalf("history mismatch: got %s want %s", outJSON, inJSON)
	}
}

func TestSaveHistoryWriteError(t *testing.T) {
	origWriteFile := osWriteFile
	t.Cleanup(func() { osWriteFile = origWriteFile })

	osWriteFile = func(string, []byte, os.FileMode) error { return errors.New("write failed") }
	err := saveHistory("ignored", historyData{Conversations: map[string][]message{}})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected write failed error, got %v", err)
	}
}

func TestHistoryLimit(t *testing.T) {
	t.Setenv("ASH_HISTORY_MAX", "")
	if got := historyLimit(); got != defaultHistoryMax {
		t.Fatalf("default limit mismatch: got %d want %d", got, defaultHistoryMax)
	}

	t.Setenv("ASH_HISTORY_MAX", "80")
	if got := historyLimit(); got != 80 {
		t.Fatalf("env limit mismatch: got %d want 80", got)
	}

	t.Setenv("ASH_HISTORY_MAX", "not-a-number")
	if got := historyLimit(); got != defaultHistoryMax {
		t.Fatalf("invalid env should fallback: got %d want %d", got, defaultHistoryMax)
	}

	t.Setenv("ASH_HISTORY_MAX", "0")
	if got := historyLimit(); got != defaultHistoryMax {
		t.Fatalf("non-positive env should fallback: got %d want %d", got, defaultHistoryMax)
	}
}

func TestKeepRecentMessages(t *testing.T) {
	messages := []message{
		{Role: "1", Content: "1"},
		{Role: "2", Content: "2"},
		{Role: "3", Content: "3"},
	}

	keptAll := keepRecentMessages(messages, 5)
	if !reflect.DeepEqual(keptAll, messages) {
		t.Fatalf("expected all messages to be kept")
	}

	trimmed := keepRecentMessages(messages, 2)
	want := []message{
		{Role: "2", Content: "2"},
		{Role: "3", Content: "3"},
	}
	if !reflect.DeepEqual(trimmed, want) {
		t.Fatalf("trimmed mismatch: got %#v want %#v", trimmed, want)
	}
}
