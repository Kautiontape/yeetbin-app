package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubServer replies to every request with the given status and body.
func stubServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCreateSendsMinimalBody(t *testing.T) {
	var body []byte
	var method, path, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		method, path = r.Method, r.URL.Path
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"x7kQ3f","url":"/x7kQ3f"}`)
	}))
	defer srv.Close()

	c := &client{baseURL: srv.URL, http: srv.Client()}
	_, err := c.create(binRequest{Content: "# hi", Type: "markdown"})
	if err != nil {
		t.Fatalf("create() error = %v, want nil", err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if path != "/api/bin" {
		t.Errorf("path = %q, want /api/bin", path)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	// Optional fields must be omitted entirely so the server applies its own defaults.
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("request body is not valid JSON: %v (%s)", err, body)
	}
	want := map[string]any{"content": "# hi", "type": "markdown"}
	if len(got) != len(want) {
		t.Errorf("body has keys %v, want exactly %v", keysOf(got), keysOf(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestCreateSendsAllFields(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id":"abc","url":"/abc"}`)
	}))
	defer srv.Close()

	c := &client{baseURL: srv.URL, http: srv.Client()}
	_, err := c.create(binRequest{
		Content:   "package main",
		Type:      "code",
		Language:  "go",
		Burn:      true,
		ExpiresAt: "2026-08-01T00:00:00Z",
		Password:  "hunter2",
	})
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	for k, want := range map[string]any{
		"content":    "package main",
		"type":       "code",
		"language":   "go",
		"burn":       true,
		"expires_at": "2026-08-01T00:00:00Z",
		"password":   "hunter2",
	} {
		if got[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, got[k], want)
		}
	}
}

func TestCreateJoinsRelativeURLOntoBase(t *testing.T) {
	srv := stubServer(t, http.StatusCreated, `{"id":"x7kQ3f","url":"/x7kQ3f"}`)

	c := &client{baseURL: srv.URL, http: srv.Client()}
	got, err := c.create(binRequest{Content: "x", Type: "text"})
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	if want := srv.URL + "/x7kQ3f"; got != want {
		t.Errorf("create() = %q, want %q", got, want)
	}
}

func TestCreateDoesNotDoubleSlashWhenBaseHasTrailingSlash(t *testing.T) {
	srv := stubServer(t, http.StatusCreated, `{"id":"abc","url":"/abc"}`)

	c := &client{baseURL: srv.URL + "/", http: srv.Client()}
	got, err := c.create(binRequest{Content: "x", Type: "text"})
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	if want := srv.URL + "/abc"; got != want {
		t.Errorf("create() = %q, want %q (no doubled slash)", got, want)
	}
}

func TestCreateFallsBackToIDWhenURLFieldMissing(t *testing.T) {
	srv := stubServer(t, http.StatusCreated, `{"id":"x7kQ3f"}`)

	c := &client{baseURL: srv.URL, http: srv.Client()}
	got, err := c.create(binRequest{Content: "x", Type: "text"})
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	if want := srv.URL + "/x7kQ3f"; got != want {
		t.Errorf("create() = %q, want %q", got, want)
	}
}

func TestCreateSurfacesServerErrorMessage(t *testing.T) {
	srv := stubServer(t, http.StatusBadRequest, `{"error":"Content is required"}`)

	c := &client{baseURL: srv.URL, http: srv.Client()}
	_, err := c.create(binRequest{Content: " ", Type: "markdown"})
	if err == nil {
		t.Fatal("create() = nil error, want the server's message")
	}
	if !strings.Contains(err.Error(), "Content is required") {
		t.Errorf("error = %q, want it to contain the server message", err)
	}
}

func TestCreateHandlesNonJSONErrorBody(t *testing.T) {
	// A proxy failure returns HTML, not JSON.
	srv := stubServer(t, http.StatusBadGateway,
		"<html><head><title>502 Bad Gateway</title></head><body>nginx</body></html>")

	c := &client{baseURL: srv.URL, http: srv.Client()}
	_, err := c.create(binRequest{Content: "x", Type: "text"})
	if err == nil {
		t.Fatal("create() = nil error, want an error for HTTP 502")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %q, want it to mention the status code", err)
	}
	if strings.Contains(err.Error(), "<html>") {
		t.Errorf("error = %q, should not dump raw HTML at the user", err)
	}
}

func TestCreateHandlesMalformedSuccessBody(t *testing.T) {
	srv := stubServer(t, http.StatusCreated, `not json at all`)

	c := &client{baseURL: srv.URL, http: srv.Client()}
	_, err := c.create(binRequest{Content: "x", Type: "text"})
	if err == nil {
		t.Fatal("create() = nil error, want an error for an unparseable 201 body")
	}
}

func TestCreateHandlesSuccessBodyWithNoID(t *testing.T) {
	srv := stubServer(t, http.StatusCreated, `{}`)

	c := &client{baseURL: srv.URL, http: srv.Client()}
	_, err := c.create(binRequest{Content: "x", Type: "text"})
	if err == nil {
		t.Fatal("create() = nil error, want an error when the server returns no id")
	}
}

func TestCreateReportsUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	c := &client{baseURL: url, http: &http.Client{Timeout: 2 * time.Second}}
	_, err := c.create(binRequest{Content: "x", Type: "text"})
	if err == nil {
		t.Fatal("create() = nil error, want a connection error")
	}
	if !strings.Contains(err.Error(), url) {
		t.Errorf("error = %q, want it to name the unreachable server %q", err, url)
	}
}

func TestParseExpiry(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"45m", 45 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"1", 0, true},        // no unit
		{"", 0, true},         // empty
		{"tomorrow", 0, true}, // not a duration
		{"0h", 0, true},       // zero is not a useful expiry
		{"-1h", 0, true},      // negative would expire in the past
		{"7days", 0, true},    // ambiguous suffix
		{"d", 0, true},        // bare suffix
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseExpiry(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseExpiry(%q) = %v, nil; want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExpiry(%q) error = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseExpiry(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
