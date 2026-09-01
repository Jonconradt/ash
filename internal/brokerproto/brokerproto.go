// Package brokerproto defines the wire protocol shared by the ash client
// (broker.go) and the ash-broker server (cmd/ash-broker) so neither binary
// needs to import the other.
package brokerproto

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	Version   = 1
	MaxFrame  = 16 << 20
	MaxBody   = 8 << 20
	MaxHeader = 64 << 10
)

// Request is the payload a client sends to the broker over the Unix socket.
type Request struct {
	Version uint16            `json:"version"`
	Token   string            `json:"token"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}

// Response is the payload the broker sends back to a client.
type Response struct {
	Version uint16 `json:"version"`
	Status  int    `json:"status"`
	Reused  bool   `json:"reused,omitempty"`
	Body    []byte `json:"body,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HeaderAllowed reports whether name may be forwarded through the broker.
func HeaderAllowed(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "authorization", "content-type", "accept", "user-agent", "anthropic-version", "x-api-key", "x-goog-api-key":
		return true
	}
	// AWS SigV4 covers these SDK-generated headers, so dropping them would
	// invalidate the signature the Bedrock adapter already computed.
	return strings.HasPrefix(lower, "x-amz-") || strings.HasPrefix(lower, "amz-sdk-")
}

// URLAllowed reports whether rawURL is a well-formed http(s) URL targeting allowedHost.
func URLAllowed(rawURL string, allowedHost string) bool {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rawURL, nil)
	return err == nil && request.URL.User == nil && request.URL.Host != "" && (request.URL.Scheme == "http" || request.URL.Scheme == "https") && strings.EqualFold(request.URL.Host, allowedHost)
}

// WriteFrame writes payload as a length-prefixed frame to w.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrame || uint64(len(payload)) > uint64(^uint32(0)) {
		return errors.New("broker frame exceeds limit")
	}
	var size [4]byte
	// #nosec G115 -- payload length is bounded by MaxFrame and uint32 maximum above.
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one length-prefixed frame from r.
func ReadFrame(r io.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(size[:])
	if length > MaxFrame {
		return nil, errors.New("broker frame exceeds limit")
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(r, payload)
	return payload, err
}
