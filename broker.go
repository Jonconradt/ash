//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	brokerSocketEnv = "ASH_BROKER_SOCKET"
	brokerTokenEnv  = "ASH_BROKER_TOKEN"
	brokerLeaseEnv  = "ASH_BROKER_LEASE"
	brokerVersion   = 1
	brokerMaxFrame  = 16 << 20
	brokerMaxBody   = 8 << 20
	brokerMaxHeader = 64 << 10
	brokerIdle      = 10 * time.Minute
)

type brokerRequest struct {
	Version uint16            `json:"version"`
	Token   string            `json:"token"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"body"`
}

type brokerResponse struct {
	Version uint16 `json:"version"`
	Status  int    `json:"status"`
	Body    []byte `json:"body,omitempty"`
	Error   string `json:"error,omitempty"`
}

func brokerConfigured() bool {
	return strings.TrimSpace(os.Getenv(brokerSocketEnv)) != "" && strings.TrimSpace(os.Getenv(brokerTokenEnv)) != ""
}

func brokerDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	socket := strings.TrimSpace(os.Getenv(brokerSocketEnv))
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if socket == "" || token == "" {
		return nil, errors.New("broker is not configured")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, brokerMaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > brokerMaxBody {
		return nil, errors.New("broker request body exceeds limit")
	}
	headers := make(map[string]string, len(req.Header))
	for name, values := range req.Header {
		if len(values) != 1 || !brokerHeaderAllowed(name) {
			return nil, fmt.Errorf("broker header %q is not allowed", name)
		}
		headers[name] = values[0]
	}
	payload, err := json.Marshal(brokerRequest{Version: brokerVersion, Token: token, URL: req.URL.String(), Headers: headers, Body: body})
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := writeBrokerFrame(conn, payload); err != nil {
		return nil, err
	}
	responsePayload, err := readBrokerFrame(conn)
	if err != nil {
		return nil, err
	}
	var response brokerResponse
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return nil, err
	}
	if response.Version != brokerVersion {
		return nil, errors.New("broker protocol version mismatch")
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return &http.Response{StatusCode: response.Status, Status: fmt.Sprintf("%d", response.Status), Body: io.NopCloser(strings.NewReader(string(response.Body))), Header: make(http.Header), Request: req}, nil
}

func brokerHeaderAllowed(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "content-type", "accept", "user-agent", "anthropic-version", "x-api-key":
		return true
	default:
		return false
	}
}

func brokerURLAllowed(rawURL string) bool {
	request, err := http.NewRequest(http.MethodPost, rawURL, nil)
	return err == nil && request.URL.User == nil && request.URL.Host != "" && (request.URL.Scheme == "http" || request.URL.Scheme == "https")
}

func writeBrokerFrame(w io.Writer, payload []byte) error {
	if len(payload) > brokerMaxFrame {
		return errors.New("broker frame exceeds limit")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readBrokerFrame(r io.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(size[:])
	if length > brokerMaxFrame {
		return nil, errors.New("broker frame exceeds limit")
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(r, payload)
	return payload, err
}

func runBroker(args []string, stdout, stderr io.Writer) int {
	var socket, lease string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--socket":
			index++
			if index >= len(args) {
				fmt.Fprintln(stderr, "--socket requires a value")
				return 2
			}
			socket = args[index]
		case "--lease":
			index++
			if index >= len(args) {
				fmt.Fprintln(stderr, "--lease requires a value")
				return 2
			}
			lease = args[index]
		default:
			fmt.Fprintf(stderr, "unknown broker option %q\n", args[index])
			return 2
		}
	}
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if lease == "" {
		lease = strings.TrimSpace(os.Getenv(brokerLeaseEnv))
	}
	if socket == "" || token == "" {
		fmt.Fprintln(stderr, "broker requires --socket and ASH_BROKER_TOKEN")
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if existing, dialErr := net.DialTimeout("unix", socket, 50*time.Millisecond); dialErr == nil {
		_ = existing.Close()
		fmt.Fprintln(stderr, "broker socket is already in use")
		return 1
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	}()
	if err := os.Chmod(socket, 0o600); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	client := newBrokerHTTPClient()
	var active sync.WaitGroup
	lastActivity := time.Now()
	var activityMu sync.Mutex
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			activityMu.Lock()
			idle := time.Since(lastActivity) >= brokerIdle
			activityMu.Unlock()
			if lease != "" {
				if info, statErr := os.Stat(lease); statErr != nil || time.Since(info.ModTime()) >= brokerIdle {
					idle = true
				}
			}
			if idle {
				_ = listener.Close()
				return
			}
		}
	}()
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			break
		}
		activityMu.Lock()
		lastActivity = time.Now()
		activityMu.Unlock()
		active.Add(1)
		go func() { defer active.Done(); handleBrokerConn(conn, token, client) }()
	}
	active.Wait()
	return 0
}

func handleBrokerConn(conn net.Conn, token string, client *http.Client) {
	defer conn.Close()
	if !brokerPeerAllowed(conn) {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(aiTimeout()))
	payload, err := readBrokerFrame(bufio.NewReader(conn))
	if err != nil {
		return
	}
	var request brokerRequest
	if json.Unmarshal(payload, &request) != nil || request.Version != brokerVersion || subtle.ConstantTimeCompare([]byte(request.Token), []byte(token)) != 1 || !brokerURLAllowed(request.URL) || len(request.Body) > brokerMaxBody {
		_ = writeBrokerFrame(conn, mustBrokerJSON(brokerResponse{Version: brokerVersion, Error: "broker request rejected"}))
		return
	}
	httpRequest, err := http.NewRequest(http.MethodPost, request.URL, strings.NewReader(string(request.Body)))
	if err == nil {
		headerBytes := 0
		for name, value := range request.Headers {
			if !brokerHeaderAllowed(name) || len(value) > brokerMaxHeader {
				err = errors.New("broker header rejected")
				break
			}
			headerBytes += len(name) + len(value)
			if headerBytes > brokerMaxHeader {
				err = errors.New("broker headers exceed limit")
				break
			}
			httpRequest.Header.Set(name, value)
		}
	}
	if err == nil {
		httpResponse, doErr := client.Do(httpRequest)
		if doErr != nil {
			err = doErr
		} else {
			body, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, brokerMaxBody+1))
			closeErr := httpResponse.Body.Close()
			if readErr != nil {
				err = readErr
			} else if closeErr != nil {
				err = closeErr
			} else if len(body) > brokerMaxBody {
				err = errors.New("broker response exceeds limit")
			} else {
				err = writeBrokerFrame(conn, mustBrokerJSON(brokerResponse{Version: brokerVersion, Status: httpResponse.StatusCode, Body: body}))
			}
		}
	}
	if err != nil {
		_ = writeBrokerFrame(conn, mustBrokerJSON(brokerResponse{Version: brokerVersion, Error: err.Error()}))
	}
}

func mustBrokerJSON(value brokerResponse) []byte { payload, _ := json.Marshal(value); return payload }

func newBrokerHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	transport.MaxConnsPerHost = 16
	transport.IdleConnTimeout = 2 * time.Minute
	return &http.Client{Transport: transport, Timeout: aiTimeout()}
}
