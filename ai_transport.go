package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// ashRoundTripper is the shared HTTP transport for provider SDK clients: it
// prefers the connection-sharing broker when configured, falls back to a
// direct dial, retries transient failures with ash's existing backoff policy,
// and records the same connect/reuse metrics chat() has always recorded. This
// lets every SDK-backed adapter reuse one retry/broker implementation instead
// of each adapter reimplementing it.
type ashRoundTripper struct {
	transport http.RoundTripper
}

// newAshHTTPClient returns an *http.Client suitable for injecting into a
// provider SDK client via its WithHTTPClient-style option.
func newAshHTTPClient() *http.Client {
	base := newHTTPClient(aiTimeout())
	return &http.Client{Timeout: aiTimeout(), Transport: &ashRoundTripper{transport: base.Transport}}
}

func (t *ashRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	var bodyBytes []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		bodyBytes = b
		_ = req.Body.Close()
	}

	attempts := retryMaxAttempts()
	baseDelay := retryBaseDelay()
	maxDelay := retryMaxDelay()
	client := &http.Client{Transport: t.transport}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptReq := req.Clone(ctx)
		if bodyBytes != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			attemptReq.ContentLength = int64(len(bodyBytes))
		}

		resp, reused, connectDuration, err := ashRoundTripAttempt(ctx, client, attemptReq, bodyBytes)
		if metrics := executionMetricsFromContext(ctx); metrics != nil {
			metrics.setConnectionReused(reused)
			metrics.addStageDuration(metricsStageConnect, connectDuration)
		}
		if err != nil {
			lastErr = err
			if !shouldRetryAIError(err, attempt, attempts) {
				return nil, err
			}
			if sleepErr := sleepWithContext(ctx, backoffDelay(attempt, baseDelay, maxDelay)); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		if !shouldRetryStatusCode(resp.StatusCode, attempt, attempts) {
			return resp, nil
		}
		_ = resp.Body.Close()
		if sleepErr := sleepWithContext(ctx, backoffDelay(attempt, baseDelay, maxDelay)); sleepErr != nil {
			return nil, sleepErr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("AI request failed after retries")
	}
	return nil, lastErr
}

// ashRoundTripAttempt performs one request attempt, preferring the broker and
// falling back to a direct dial (rebuilt from bodyBytes, since brokerDo
// consumes req's body) when the broker is unavailable or rejects the request.
func ashRoundTripAttempt(ctx context.Context, client *http.Client, req *http.Request, bodyBytes []byte) (*http.Response, bool, time.Duration, error) {
	if brokerConfigured() {
		resp, reused, err := brokerDo(ctx, req)
		if err == nil {
			return resp, reused, 0, nil
		}
		slog.Debug("broker request failed, falling back to direct dial", "request_id", requestIDFromContext(ctx), "error", err, "EID", "Lp8xKd3W")
		fallbackReq, buildErr := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(bodyBytes)) // #nosec G704 -- fallbackReq targets the same user-configured AI_ENDPOINT as req, not attacker-controlled input.
		if buildErr != nil {
			return nil, false, 0, buildErr
		}
		fallbackReq.Header = req.Header.Clone()
		return httpClientDoWithReuse(ctx, client, fallbackReq)
	}
	return httpClientDoWithReuse(ctx, client, req)
}
