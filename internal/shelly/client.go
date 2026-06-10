package shelly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// defaultHTTPClient bounds calls to unresponsive devices even when the
// caller forgets a context deadline.
var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

// Client talks JSON-RPC to one Shelly Gen2+ device at http://<host>/rpc.
// Safe for concurrent use. Digest auth (SHA-256, user "admin") is handled
// transparently when a password is configured.
type Client struct {
	host     string
	hc       *http.Client
	password string

	mu   sync.Mutex
	auth *digestState // non-nil once a challenge has been answered
}

// Option configures a Client.
type Option func(*Client)

// WithPassword enables digest auth with the device admin password.
func WithPassword(pw string) Option { return func(c *Client) { c.password = pw } }

// WithHTTPClient overrides the default client (e.g. to set a shorter
// Timeout). A nil hc is ignored.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.hc = hc
		}
	}
}

// NewClient creates a client for a device host ("10.32.8.38" or "host:port").
func NewClient(host string, opts ...Option) *Client {
	c := &Client{host: host, hc: defaultHTTPClient}
	for _, o := range opts {
		o(c)
	}
	return c
}

// RPCError is a JSON-RPC error returned by the device.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("shelly rpc error %d: %s", e.Code, e.Message)
}

// AuthError indicates the device rejected our credentials (or requires
// credentials we don't have).
type AuthError struct{ Host string }

func (e *AuthError) Error() string { return "shelly: authentication failed for " + e.Host }

type rpcRequest struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// Call invokes an RPC method and unmarshals its result (result may be nil).
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	payload, err := json.Marshal(rpcRequest{ID: 1, Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, payload)
	if err != nil {
		var authErr *AuthError
		if errors.As(err, &authErr) {
			return authErr
		}
		return fmt.Errorf("shelly: %s %s: %w", method, c.host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return &AuthError{Host: c.host}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("shelly: %s %s: status %s: %s", method, c.host, resp.Status, body)
	}
	var rr rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return fmt.Errorf("shelly: %s %s: decode: %w", method, c.host, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection returns to the pool
	if rr.Error != nil {
		return fmt.Errorf("shelly: %s %s: %w", method, c.host, rr.Error)
	}
	if result != nil && len(rr.Result) > 0 {
		if err := json.Unmarshal(rr.Result, result); err != nil {
			return fmt.Errorf("shelly: %s %s: unmarshal result: %w", method, c.host, err)
		}
	}
	return nil
}

// post sends the payload, answering one digest challenge if the device
// returns 401 and we have a password (see digest.go).
func (c *Client) post(ctx context.Context, payload []byte) (*http.Response, error) {
	resp, err := c.send(ctx, payload, c.authHeader())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized || c.password == "" {
		return resp, nil
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the retry reuses the connection
	_ = resp.Body.Close()
	if err := c.setChallenge(challenge); err != nil {
		return nil, err
	}
	return c.send(ctx, payload, c.authHeader())
}

func (c *Client) send(ctx context.Context, payload []byte, authHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.host+"/rpc", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return c.hc.Do(req)
}
