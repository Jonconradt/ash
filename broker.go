//go:build darwin || freebsd || linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"ash/internal/brokerproto"
)

const (
	brokerSocketEnv = "ASH_BROKER_SOCKET"
	brokerTokenEnv  = "ASH_BROKER_TOKEN"
	brokerLeaseEnv  = "ASH_BROKER_LEASE"
)

func brokerConfigured() bool {
	return strings.TrimSpace(os.Getenv(brokerSocketEnv)) != "" && strings.TrimSpace(os.Getenv(brokerTokenEnv)) != ""
}

func brokerDo(ctx context.Context, req *http.Request) (*http.Response, bool, error) {
	socket := strings.TrimSpace(os.Getenv(brokerSocketEnv))
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if socket == "" || token == "" {
		return nil, false, errors.New("broker is not configured")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, brokerproto.MaxBody+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > brokerproto.MaxBody {
		return nil, false, errors.New("broker request body exceeds limit")
	}
	// Provider SDKs attach telemetry headers (x-stainless-*, x-fern-*, ...) that
	// the broker deliberately does not forward. Drop them like the broker server
	// does instead of failing the request, which would silently fall back to a
	// fresh direct dial and defeat connection reuse.
	headers := make(map[string]string, len(req.Header))
	var dropped []string
	for name, values := range req.Header {
		if len(values) != 1 || !brokerproto.HeaderAllowed(name) {
			dropped = append(dropped, name)
			continue
		}
		headers[name] = values[0]
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		slog.Debug("broker dropped unsupported headers", "request_id", requestIDFromContext(ctx), "headers", strings.Join(dropped, ","), "EID", "Qm4vRb7T")
	}
	payload, err := json.Marshal(brokerproto.Request{Version: brokerproto.Version, Token: token, URL: req.URL.String(), Headers: headers, Body: body})
	if err != nil {
		return nil, false, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = conn.Close() }()
	brokerDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-brokerDone:
		}
	}()
	defer close(brokerDone)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := brokerproto.WriteFrame(conn, payload); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, ctxErr
		}
		return nil, false, err
	}
	responsePayload, err := brokerproto.ReadFrame(conn)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, ctxErr
		}
		return nil, false, err
	}
	var response brokerproto.Response
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return nil, false, err
	}
	if response.Version != brokerproto.Version {
		return nil, false, errors.New("broker protocol version mismatch")
	}
	if response.Error != "" {
		return nil, false, errors.New(response.Error)
	}
	header := make(http.Header)
	if response.ContentType != "" {
		header.Set("Content-Type", response.ContentType)
	}
	return &http.Response{StatusCode: response.Status, Status: strconv.Itoa(response.Status), Body: io.NopCloser(strings.NewReader(string(response.Body))), Header: header, Request: req}, response.Reused, nil
}
