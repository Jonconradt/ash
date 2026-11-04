// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jonconradt/ash/internal/brokerproto"
)

func TestToolCapabilityCachePersistsHashedEndpointAndModel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capabilities")
	now := time.Date(2026, time.November, 4, 20, 0, 0, 0, time.UTC)
	cache := newToolCapabilityCache(dir)
	cache.now = func() time.Time { return now }

	endpoint := "https://private.example:11434/v1?token=do-not-store"
	model := "gemma4:latest"
	if err := cache.markUnsupported(endpoint, model); err != nil {
		t.Fatalf("markUnsupported returned error: %v", err)
	}

	restartedCache := newToolCapabilityCache(dir)
	restartedCache.now = func() time.Time { return now }
	unsupported, err := restartedCache.unsupported(endpoint, model)
	if err != nil {
		t.Fatalf("unsupported returned error: %v", err)
	}
	if !unsupported {
		t.Fatal("expected unsupported capability to persist across cache instances")
	}
	otherModel, err := restartedCache.unsupported(endpoint, "llama3.1")
	if err != nil {
		t.Fatalf("unsupported for other model returned error: %v", err)
	}
	if otherModel {
		t.Fatal("unsupported capability must be scoped to the endpoint/model pair")
	}

	path, err := restartedCache.recordPath(endpoint, model)
	if err != nil {
		t.Fatalf("recordPath returned error: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache record: %v", err)
	}
	if strings.Contains(string(content), "private.example") || strings.Contains(string(content), model) || strings.Contains(string(content), "do-not-store") {
		t.Fatalf("cache record leaked endpoint or model data: %s", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache record: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache record permissions = %o, want 600", info.Mode().Perm())
	}

	now = now.Add(toolCapabilityCacheTTL + time.Second)
	expired, err := restartedCache.unsupported(endpoint, model)
	if err != nil {
		t.Fatalf("expired unsupported lookup returned error: %v", err)
	}
	if expired {
		t.Fatal("expired capability record must not be used")
	}
}

func TestHandleToolCapabilityRequestValidatesEndpointAndPersists(t *testing.T) {
	cache := newToolCapabilityCache(t.TempDir())
	const endpoint = "http://ollama.example:11434"
	request := brokerproto.Request{
		ToolCacheAction:   "lookup",
		ToolCacheEndpoint: endpoint,
		ToolCacheModel:    "gemma4:latest",
	}

	response := handleToolCapabilityRequest(request, "ollama.example:11434", cache)
	if response.Error != "" || response.ToolUnsupported {
		t.Fatalf("initial lookup = %#v, want unsupported false", response)
	}

	request.ToolCacheAction = "mark_unsupported"
	response = handleToolCapabilityRequest(request, "ollama.example:11434", cache)
	if response.Error != "" {
		t.Fatalf("mark unsupported failed: %s", response.Error)
	}

	request.ToolCacheAction = "lookup"
	response = handleToolCapabilityRequest(request, "ollama.example:11434", cache)
	if response.Error != "" || !response.ToolUnsupported {
		t.Fatalf("lookup after mark = %#v, want unsupported true", response)
	}

	request.ToolCacheEndpoint = "https://attacker.example:11434"
	response = handleToolCapabilityRequest(request, "ollama.example:11434", cache)
	if response.Error == "" {
		t.Fatal("expected a capability request for another host to be rejected")
	}
}
