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

// Optional fields are omitted so the server applies its own defaults.
type binRequest struct {
	Content   string `json:"content"`
	Type      string `json:"type"`
	Language  string `json:"language,omitempty"`
	Burn      bool   `json:"burn,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Password  string `json:"password,omitempty"`
}

// URL is relative to the instance root.
type binResponse struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type client struct {
	baseURL string
	http    *http.Client
}

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
		return "", fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer resp.Body.Close()

	// A proxy error page can be arbitrarily large.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("reading response from %s: %w", base, err)
	}

	var parsed binResponse
	jsonErr := json.Unmarshal(raw, &parsed)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		// Never surface the raw body; it may be an HTML error page.
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

// Accepts any Go duration plus an "Nd" day form, which time.ParseDuration rejects.
func parseExpiry(s string) (time.Duration, error) {
	invalid := fmt.Errorf("invalid expiry %q: use a duration like 1h, 24h, 7d or 30d", s)
	if s == "" {
		return 0, invalid
	}

	d, err := time.ParseDuration(s)
	if err != nil {
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
