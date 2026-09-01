package brokerproto

import (
	"strings"
	"testing"
)

func TestReadFrameRejectsOversizedFrame(t *testing.T) {
	if _, err := ReadFrame(strings.NewReader("\xff\xff\xff\xff")); err == nil {
		t.Fatal("expected oversized frame to be rejected")
	}
}

func TestURLAllowedPinsToConfiguredHost(t *testing.T) {
	if !URLAllowed("https://api.example.com/v1/chat", "api.example.com") {
		t.Fatal("expected matching host to be allowed")
	}
	if URLAllowed("https://attacker.example.com/v1/chat", "api.example.com") {
		t.Fatal("expected mismatched host to be rejected")
	}
	if URLAllowed("https://user@api.example.com/v1/chat", "api.example.com") {
		t.Fatal("expected embedded userinfo to be rejected")
	}
}
