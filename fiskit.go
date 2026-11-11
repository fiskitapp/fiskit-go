// Package fiskit is the official Go client for the FisKit API: Argentine electronic invoices with
// ARCA (formerly AFIP) in a single request. It has no dependencies besides the standard library.
//
//	client, err := fiskit.New("") // reads FISKIT_API_KEY
//	invoice, err := client.Invoices.Create(ctx, map[string]any{
//		"voucher_type": "invoice_c",
//		"amounts":      map[string]any{"total": 1500},
//	}, "order-8812")
package fiskit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Version of this library, sent in the User-Agent header.
const Version = "0.1.1"

const defaultBaseURL = "https://api.fiskit.com.ar"

// Client talks to the FisKit API. It is safe for concurrent use.
type Client struct {
	apiKey     string
	baseURL    string
	maxRetries int
	http       *http.Client

	Invoices  *InvoicesService
	Taxpayers *TaxpayersService
}

// Option customizes a Client.
type Option func(*Client)

// WithBaseURL points the client to another address, for example a mock in tests.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithHTTPClient replaces the HTTP client (timeouts, proxies, instrumentation).
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.http = httpClient }
}

// WithMaxRetries sets how many times a failed request is retried (2 by default). Only requests that
// are safe to repeat are retried: GETs and POSTs with an idempotency key.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = n }
}

// New creates a client. apiKey is sk_test_... or sk_live_...; when empty, FISKIT_API_KEY is used.
func New(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("FISKIT_API_KEY")
	}
	if apiKey == "" {
		return nil, errors.New("fiskit: missing API key: pass it or set FISKIT_API_KEY")
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		maxRetries: 2,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	c.Invoices = &InvoicesService{client: c}
	c.Taxpayers = &TaxpayersService{client: c}
	return c, nil
}

// Error is an error answered by the API, or a network failure (Status 0, Code "network").
// Branch on Code, which is stable; Message is meant for people.
type Error struct {
	Status    int
	Type      string
	Code      string
	Message   string
	Param     string
	RequestID string
	Body      []byte
}

func (e *Error) Error() string {
	if e.Param != "" {
		return fmt.Sprintf("fiskit: %s (%s, param %s)", e.Message, e.Code, e.Param)
	}
	return fmt.Sprintf("fiskit: %s (%s)", e.Message, e.Code)
}

var retryable = map[int]bool{408: true, 409: true, 429: true, 500: true, 502: true, 503: true, 504: true}

type request struct {
	method         string
	path           string
	body           any
	idempotencyKey string
	accept         string
}

// do sends the request with retries and returns the body of a 2xx response.
func (c *Client) do(ctx context.Context, r request) ([]byte, error) {
	var payload []byte
	if r.body != nil {
		var err error
		if payload, err = json.Marshal(r.body); err != nil {
			return nil, fmt.Errorf("fiskit: encoding the request: %w", err)
		}
	}
	accept := r.accept
	if accept == "" {
		accept = "application/json"
	}
	// Without an idempotency key a POST is not repeated: it might have been processed
	safe := r.method == http.MethodGet || r.idempotencyKey != ""

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, r.method, c.baseURL+r.path, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("fiskit: building the request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", "fiskit-go/"+Version)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if r.idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", r.idempotencyKey)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() == nil && safe && attempt < c.maxRetries {
				if err := sleep(ctx, attempt, ""); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &Error{Status: 0, Type: "connection_error", Code: "network", Message: err.Error()}
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, &Error{Status: 0, Type: "connection_error", Code: "network", Message: readErr.Error()}
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		if retryable[resp.StatusCode] && safe && attempt < c.maxRetries {
			if err := sleep(ctx, attempt, resp.Header.Get("Retry-After")); err != nil {
				return nil, err
			}
			continue
		}
		return nil, apiError(resp, body)
	}
}

func apiError(resp *http.Response, body []byte) *Error {
	var envelope struct {
		Error struct {
			Type      string `json:"type"`
			Code      string `json:"code"`
			Message   string `json:"message"`
			Param     string `json:"param"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	e := &Error{
		Status:    resp.StatusCode,
		Type:      envelope.Error.Type,
		Code:      envelope.Error.Code,
		Message:   envelope.Error.Message,
		Param:     envelope.Error.Param,
		RequestID: envelope.Error.RequestID,
		Body:      body,
	}
	if e.Type == "" {
		e.Type = "api_error"
	}
	if e.Code == "" {
		e.Code = "http_" + strconv.Itoa(resp.StatusCode)
	}
	if e.Message == "" {
		e.Message = "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	if e.RequestID == "" {
		e.RequestID = resp.Header.Get("Request-Id")
	}
	return e
}

func sleep(ctx context.Context, attempt int, retryAfter string) error {
	wait := time.Duration(500<<attempt) * time.Millisecond
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		if seconds > 30 {
			seconds = 30
		}
		wait = time.Duration(seconds) * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
