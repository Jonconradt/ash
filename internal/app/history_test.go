package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGetHistoryPath(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("ASH_SESSION_ID", "abc123")
	t.Setenv("ASH_SCHEDULED_TASK", "")

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}

	want := filepath.Join(home, ".local", "state", "ash", historyDirName, "abc123.json")
	if path != want {
		t.Fatalf("path mismatch: got %q want %q", path, want)
	}
}

func TestGetHistoryPathScheduled(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("ASH_SESSION_ID", "abc123")
	t.Setenv("ASH_SCHEDULED_TASK", "1")

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}

	want := filepath.Join(home, ".local", "state", "ash", historyDirName, "task_abc123.json")
	if path != want {
		t.Fatalf("path mismatch: got %q want %q", path, want)
	}
}

func TestGetHistoryPathPreservesChildSessionID(t *testing.T) {
	origHome := osUserHomeDir
	t.Cleanup(func() { osUserHomeDir = origHome })

	home := t.TempDir()
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Setenv("ASH_SESSION_ID", "parent.abc123")
	t.Setenv("ASH_SCHEDULED_TASK", "")

	path, err := getHistoryPath()
	if err != nil {
		t.Fatalf("getHistoryPath returned error: %v", err)
	}
	want := filepath.Join(home, ".local", "state", "ash", historyDirName, "parent.abc123.json")
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
		t.Setenv("ASH_AI_TIMEOUT", "45s")
		if got := aiTimeout(); got != 45*time.Second {
			t.Fatalf("aiTimeout mismatch: got %s want %s", got, 45*time.Second)
		}
	})

	t.Run("invalid falls back", func(t *testing.T) {
		t.Setenv("ASH_AI_TIMEOUT", "not-a-duration")
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
	if data.Version != executionDashboardVersion() {
		t.Fatalf("expected current version %q, got %q", executionDashboardVersion(), data.Version)
	}
	if data.SchemaVersion != historySchemaVersion {
		t.Fatalf("expected schema version %d, got %d", historySchemaVersion, data.SchemaVersion)
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

	osReadFile = func(string) ([]byte, error) {
		return []byte(`{"schema_version":1,"conversations":null}`), nil
	}
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
	t.Cleanup(func() { osReadFile = origReadFile })

	path := filepath.Join(t.TempDir(), "history.json")
	input := historyData{
		SchemaVersion: historySchemaVersion,
		Version:       executionDashboardVersion(),
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
	path := filepath.Join(t.TempDir(), "missing", "history.json")
	err := saveHistory(path, historyData{Conversations: map[string][]message{}})
	if err == nil {
		t.Fatal("expected an error when the history directory does not exist")
	}
}

func TestLoadHistoryRejectsUnsupportedSchema(t *testing.T) {
	for _, schemaVersion := range []int{0, historySchemaVersion + 1} {
		t.Run(strconv.Itoa(schemaVersion), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.json")
			content := []byte(fmt.Sprintf(`{"schema_version":%d,"conversations":{}}`, schemaVersion))
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatalf("write history fixture: %v", err)
			}
			_, err := loadHistory(path)
			if err == nil || !strings.Contains(err.Error(), "unsupported history schema version") {
				t.Fatalf("expected unsupported schema error, got %v", err)
			}
		})
	}
}

func TestSaveHistoryWritesAtomicallyWithPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("write initial history: %v", err)
	}
	if err := saveHistory(path, historyData{Conversations: map[string][]message{}}); err != nil {
		t.Fatalf("saveHistory returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved history: %v", err)
	}
	var got historyData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("history file is not valid JSON: %v", err)
	}
	if got.SchemaVersion != historySchemaVersion {
		t.Fatalf("schema version = %d, want %d", got.SchemaVersion, historySchemaVersion)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat saved history: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history permissions = %o, want 600", info.Mode().Perm())
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
