// Package rewe is a client for the (reverse-engineered) REWE mobile API.
package rewe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// BaseURL of the REWE mobile clients API. KORB_API_URL overrides it (e.g. for korb-fake).
var BaseURL = envOr("KORB_API_URL", "https://mobile-clients-api.rewe.de/api")

// ReloadBaseURL re-reads KORB_API_URL (for tests that change the environment).
func ReloadBaseURL() { BaseURL = envOr("KORB_API_URL", "https://mobile-clients-api.rewe.de/api") }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

// NewHTTPClient builds an HTTP/2 client presenting the REWE app's mTLS client
// certificate. Go's TLS only sends it to servers that ask for one, so the same
// client is also used for account.rewe.de.
func NewHTTPClient(certPEM, keyPEM []byte) (*http.Client, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading REWE client certificate: %w", err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	tr.ForceAttemptHTTP2 = true
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}, nil
}

// APIError is a non-2xx response or an error reported inside the response body.
type APIError struct {
	Method string
	URL    string
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Msg)
}

// Request describes one HTTP call.
type Request struct {
	Method string
	URL    string
	Query  url.Values
	Header http.Header
	JSON   any        // JSON-encoded body if non-nil
	Form   url.Values // form-encoded body if non-nil
}

// Doer executes requests; shared by the API client and the auth flow.
type Doer struct {
	HTTP      *http.Client
	UserAgent string
}

// Do executes r and returns the body of a 2xx response.
func (d *Doer) Do(ctx context.Context, r Request) ([]byte, error) {
	u := r.URL
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var body io.Reader
	var contentType string
	switch {
	case r.Form != nil:
		body = strings.NewReader(r.Form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case r.JSON != nil:
		b, err := json.Marshal(r.JSON)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("Accept") == "" {
		// Like the REWE app; some endpoints (e.g. /baskets) answer 406 to "application/json".
		req.Header.Set("Accept", "*/*")
	}
	if d.UserAgent != "" {
		req.Header.Set("User-Agent", d.UserAgent)
	}
	res, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, &APIError{Method: r.Method, URL: r.URL, Status: res.StatusCode, Msg: summarizeBody(res, data)}
	}
	return data, nil
}

func summarizeBody(res *http.Response, data []byte) string {
	if strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		if res.StatusCode == http.StatusForbidden {
			return "request blocked by REWE's web application firewall"
		}
		return "unexpected HTML response"
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 600 {
		s = s[:600] + "..."
	}
	if s == "" {
		s = http.StatusText(res.StatusCode)
	}
	return s
}

// envelope is REWE's GraphQL-style response wrapper.
type envelope[T any] struct {
	Data   *T `json:"data"`
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			StatusCode int `json:"statusCode"`
		} `json:"extensions"`
	} `json:"errors"`
}

// call performs a request and unwraps {"data": ...} into T. Errors reported in
// the body (REWE answers HTTP 200 with an "errors" array) are turned into APIErrors.
func call[T any](ctx context.Context, d *Doer, r Request) (*T, error) {
	raw, err := d.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	var env envelope[T]
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &APIError{Method: r.Method, URL: r.URL, Status: 200, Msg: "decoding response: " + err.Error()}
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, 0, len(env.Errors))
		status := 200
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
			if e.Extensions.StatusCode != 0 {
				status = e.Extensions.StatusCode
			}
		}
		return nil, &APIError{Method: r.Method, URL: r.URL, Status: status, Msg: strings.Join(msgs, "; ")}
	}
	if env.Data == nil {
		return nil, &APIError{Method: r.Method, URL: r.URL, Status: 200, Msg: "response contained no data"}
	}
	return env.Data, nil
}
