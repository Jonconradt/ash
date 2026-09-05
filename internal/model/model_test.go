package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelSerialization(t *testing.T) {
	msg := Message{
		Role:    "user",
		Content: "Hello world",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Role != msg.Role || decoded.Content != msg.Content {
		t.Errorf("Decoded mismatch: got %#v, want %#v", decoded, msg)
	}
}

func TestModelAdversarialPayloads(t *testing.T) {
	malformedPayloads := []string{
		`{"role": null, "content": null}`,
		`{"role": 12345, "content": ["nested", "array"]}`,
		`{"unknown_field": "injected", "role": "system", "content": "\x00\u0000"}`,
		`{"role": "` + strings.Repeat("A", 10000) + `", "content": "` + strings.Repeat("B", 10000) + `"}`,
		`{}`,
	}

	for i, payload := range malformedPayloads {
		var msg Message
		_ = json.Unmarshal([]byte(payload), &msg)
		// Marshal back out to verify no panic
		_, _ = json.Marshal(msg)
		if i == 0 && msg.Role != "" {
			t.Errorf("expected empty role for null input")
		}
	}
}
