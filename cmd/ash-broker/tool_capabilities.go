// Copyright (c) 2026 Jonathan L. Conradt
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	toolCapabilityCacheVersion    = 1
	toolCapabilityCacheTTL        = 7 * 24 * time.Hour
	maxToolCapabilityEndpointSize = 4096
	maxToolCapabilityModelSize    = 1024
)

type toolCapabilityCache struct {
	dir string
	now func() time.Time
}

type toolCapabilityRecord struct {
	Version   int       `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
}

func newToolCapabilityCache(dir string) *toolCapabilityCache {
	return &toolCapabilityCache{dir: dir, now: time.Now}
}

func toolCapabilityCacheKey(endpoint, model string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = ""
		parsed.Fragment = ""
		endpoint = parsed.String()
	}
	sum := sha256.Sum256([]byte(endpoint + "\x00" + strings.TrimSpace(model)))
	return hex.EncodeToString(sum[:])
}

func (c *toolCapabilityCache) unsupported(endpoint, model string) (bool, error) {
	path, err := c.recordPath(endpoint, model)
	if err != nil {
		return false, err
	}
	// #nosec G304 -- path is a hash-derived file name joined under the cache directory by recordPath.
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read tool capability cache: %w", err)
	}
	var record toolCapabilityRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return false, fmt.Errorf("decode tool capability cache: %w", err)
	}
	if record.Version != toolCapabilityCacheVersion {
		return false, fmt.Errorf("unsupported tool capability cache version %d", record.Version)
	}
	return c.now().Before(record.ExpiresAt), nil
}

func (c *toolCapabilityCache) markUnsupported(endpoint, model string) error {
	path, err := c.recordPath(endpoint, model)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return fmt.Errorf("create tool capability cache directory: %w", err)
	}
	// #nosec G302 -- Directories require execute permission; 0700 restricts access to the owner.
	if err := os.Chmod(c.dir, 0o700); err != nil {
		return fmt.Errorf("set tool capability cache directory permissions: %w", err)
	}
	content, err := json.Marshal(toolCapabilityRecord{
		Version:   toolCapabilityCacheVersion,
		ExpiresAt: c.now().Add(toolCapabilityCacheTTL),
	})
	if err != nil {
		return fmt.Errorf("encode tool capability cache: %w", err)
	}
	temp, err := os.CreateTemp(c.dir, ".tool-capability-*")
	if err != nil {
		return fmt.Errorf("create tool capability cache file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set tool capability cache permissions: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write tool capability cache: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync tool capability cache: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close tool capability cache: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace tool capability cache: %w", err)
	}
	return nil
}

func (c *toolCapabilityCache) recordPath(endpoint, model string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" || len(endpoint) > maxToolCapabilityEndpointSize {
		return "", errors.New("tool capability endpoint is invalid")
	}
	if model == "" || len(model) > maxToolCapabilityModelSize {
		return "", errors.New("tool capability model is invalid")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("tool capability endpoint is invalid")
	}
	return filepath.Join(c.dir, toolCapabilityCacheKey(endpoint, model)+".json"), nil
}
