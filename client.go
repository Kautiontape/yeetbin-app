package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// binRequest is the body of POST /api/bin. Optional fields are omitted so the server
// applies its own defaults rather than us restating them.
type binRequest struct {
	Content   string `json:"content"`
	Type      string `json:"type"`
	Language  string `json:"language,omitempty"`
	Burn      bool   `json:"burn,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Password  string `json:"password,omitempty"`
}

// binResponse is the 201 body. url is relative to the instance root.
type binResponse struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type client struct {
	baseURL string
	http    *http.Client
}

// create uploads a bin and returns its absolute URL.
func (c *client) create(req binRequest) (string, error) {
	base := strings.TrimRight(c.baseURL, "/")

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("encoding request: %w", err)
	}

	endpoint := base + "/api/bin"
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// http.Client errors already embed the URL, but not always the bare base, so
		// name the target explicitly to make a wrong YEET_URL obvious.
		return "", fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer resp.Body.Close()

	// Cap the read: an error page from a proxy can be arbitrarily large.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("reading response from %s: %w", base, err)
	}

	var parsed binResponse
	jsonErr := json.Unmarshal(raw, &parsed)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		// Prefer the server's own message. Fall back to the status line, never the
		// raw body, which may be an HTML error page.
		if jsonErr == nil && parsed.Error != "" {
			return "", fmt.Errorf("server rejected the upload (%d): %s", resp.StatusCode, parsed.Error)
		}
		return "", fmt.Errorf("server returned %d %s", resp.StatusCode,
			http.StatusText(resp.StatusCode))
	}

	if jsonErr != nil {
		return "", fmt.Errorf("unexpected response from %s: not valid JSON", base)
	}
	if parsed.ID == "" && parsed.URL == "" {
		return "", fmt.Errorf("unexpected response from %s: no bin id", base)
	}

	path := parsed.URL
	if path == "" {
		path = "/" + parsed.ID
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}

// parseExpiry accepts any Go duration plus an "Nd" day form, which time.ParseDuration
// does not support. The result must be positive: a zero or negative expiry would create
// a bin that is already gone.
func parseExpiry(s string) (time.Duration, error) {
	invalid := fmt.Errorf("invalid expiry %q: use a duration like 1h, 24h, 7d or 30d", s)
	if s == "" {
		return 0, invalid
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		// Retry as a day count, e.g. "7d".
		rest, ok := strings.CutSuffix(s, "d")
		if !ok || rest == "" {
			return 0, invalid
		}
		days, convErr := strconv.ParseFloat(rest, 64)
		if convErr != nil {
			return 0, invalid
		}
		d = time.Duration(days * 24 * float64(time.Hour))
	}

	if d <= 0 {
		return 0, fmt.Errorf("invalid expiry %q: must be positive", s)
	}
	return d, nil
}
