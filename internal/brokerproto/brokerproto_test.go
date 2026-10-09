package brokerproto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPReadinessFieldsAreAdditive(t *testing.T) {
	var oldRequest Request
	if err := json.Unmarshal([]byte(`{"version":1,"mcp_action":"start","mcp_server":"remote"}`), &oldRequest); err != nil {
		t.Fatalf("decoding old request: %v", err)
	}
	if oldRequest.MCPIncludeTools {
		t.Fatal("old request unexpectedly requested tool readiness")
	}

	var oldResponse Response
	if err := json.Unmarshal([]byte(`{"version":1,"mcp_status":{"state":"connected"}}`), &oldResponse); err != nil {
		t.Fatalf("decoding old response: %v", err)
	}
	if oldResponse.MCPToolsReady {
		t.Fatal("old response unexpectedly reports tool readiness")
	}

	want := Response{Version: Version, MCPTools: []MCPTool{}, MCPToolsReady: true}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encoding ready empty catalog: %v", err)
	}
	var got Response
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decoding ready empty catalog: %v", err)
	}
	if !got.MCPToolsReady || len(got.MCPTools) != 0 {
		t.Fatalf("ready empty catalog = ready %t, tools %#v", got.MCPToolsReady, got.MCPTools)
	}
}

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
