package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// harness wires run() to a stub server and replaces every function that would touch the
// real terminal or a browser. Originals are restored on cleanup.
type harness struct {
	srv      *httptest.Server
	requests [][]byte
	opened   []string
	stdout   bytes.Buffer
	stderr   bytes.Buffer
}

func newHarness(t *testing.T, status int, respBody string) *harness {
	t.Helper()
	h := &harness{}

	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		h.requests = append(h.requests, buf.Bytes())
		w.WriteHeader(status)
		fmt.Fprint(w, respBody)
	}))
	t.Cleanup(h.srv.Close)

	t.Setenv("YEET_URL", h.srv.URL)
	t.Setenv("YEET_PASSWORD", "")

	origOpen, origStdoutTTY, origStdinTTY, origPass := openInBrowser, stdoutIsTTY, stdinIsTTY, readPassword
	t.Cleanup(func() {
		openInBrowser, stdoutIsTTY, stdinIsTTY, readPassword = origOpen, origStdoutTTY, origStdinTTY, origPass
	})

	openInBrowser = func(url string) error { h.opened = append(h.opened, url); return nil }
	stdoutIsTTY = func() bool { return false }
	stdinIsTTY = func() bool { return false }
	readPassword = func(prompt string) (string, error) {
		return "", fmt.Errorf("readPassword called unexpectedly")
	}
	return h
}

func okHarness(t *testing.T) *harness {
	return newHarness(t, http.StatusCreated, `{"id":"x7kQ3f","url":"/x7kQ3f"}`)
}

// run invokes the CLI with the given args and stdin text.
func (h *harness) run(args []string, stdin string) int {
	return run(args, strings.NewReader(stdin), &h.stdout, &h.stderr)
}

// body decodes the nth request body sent to the server.
func (h *harness) body(t *testing.T, n int) map[string]any {
	t.Helper()
	if len(h.requests) <= n {
		t.Fatalf("wanted request #%d but only %d were sent", n, len(h.requests))
	}
	var m map[string]any
	if err := json.Unmarshal(h.requests[n], &m); err != nil {
		t.Fatalf("request #%d is not valid JSON: %v", n, err)
	}
	return m
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunUploadsFileAndPrintsURLToStdout(t *testing.T) {
	h := okHarness(t)
	path := writeFile(t, "notes.md", "# Hello\n")

	if code := h.run([]string{path}, ""); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}

	wantURL := h.srv.URL + "/x7kQ3f"
	if got := h.stdout.String(); got != wantURL+"\n" {
		t.Errorf("stdout = %q, want %q", got, wantURL+"\n")
	}
	if h.stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on success", h.stderr.String())
	}

	body := h.body(t, 0)
	if body["content"] != "# Hello\n" {
		t.Errorf("content = %q, want the file contents verbatim", body["content"])
	}
	if body["type"] != "markdown" {
		t.Errorf("type = %v, want markdown", body["type"])
	}
}

func TestRunDetectsCodeTypeFromExtension(t *testing.T) {
	h := okHarness(t)
	path := writeFile(t, "server.go", "package main\n")

	if code := h.run([]string{path}, ""); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	body := h.body(t, 0)
	if body["type"] != "code" || body["language"] != "go" {
		t.Errorf("got type=%v language=%v, want code/go", body["type"], body["language"])
	}
}

func TestRunReadsStdinWhenNoFileArgument(t *testing.T) {
	h := okHarness(t)

	if code := h.run(nil, "piped content\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	body := h.body(t, 0)
	if body["content"] != "piped content\n" {
		t.Errorf("content = %q, want the piped text", body["content"])
	}
	if body["type"] != "markdown" {
		t.Errorf("type = %v, want markdown for stdin", body["type"])
	}
}

func TestRunReadsStdinWithDashArgument(t *testing.T) {
	h := okHarness(t)
	stdinIsTTY = func() bool { return true } // explicit "-" wins over the TTY check

	if code := h.run([]string{"-"}, "explicit stdin\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if body := h.body(t, 0); body["content"] != "explicit stdin\n" {
		t.Errorf("content = %q", body["content"])
	}
}

func TestRunTypeFlagOverridesDetection(t *testing.T) {
	h := okHarness(t)
	path := writeFile(t, "notes.md", "# not really markdown\n")

	if code := h.run([]string{"--type", "text", path}, ""); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if body := h.body(t, 0); body["type"] != "text" {
		t.Errorf("type = %v, want text (flag must beat the .md extension)", body["type"])
	}
}

func TestRunLangFlagImpliesCodeType(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"--lang", "python"}, "print('hi')\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	body := h.body(t, 0)
	if body["type"] != "code" || body["language"] != "python" {
		t.Errorf("got type=%v language=%v, want code/python", body["type"], body["language"])
	}
}

func TestRunBurnFlagIsSent(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"--burn"}, "secret\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if body := h.body(t, 0); body["burn"] != true {
		t.Errorf("burn = %v, want true", body["burn"])
	}
}

func TestRunExpireFlagSendsFutureTimestamp(t *testing.T) {
	h := okHarness(t)
	before := time.Now().UTC()

	if code := h.run([]string{"--expire", "7d"}, "temporary\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}

	raw, ok := h.body(t, 0)["expires_at"].(string)
	if !ok {
		t.Fatalf("expires_at missing or not a string: %v", h.body(t, 0)["expires_at"])
	}
	got, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", raw, err)
	}
	want := before.Add(7 * 24 * time.Hour)
	if diff := got.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expires_at = %v, want ~%v (off by %v)", got, want, diff)
	}
}

func TestRunOmitsExpiresAtByDefault(t *testing.T) {
	h := okHarness(t)

	if code := h.run(nil, "permanent\n"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, present := h.body(t, 0)["expires_at"]; present {
		t.Error("expires_at was sent without --expire; bins should default to permanent")
	}
}

func TestRunOpensBrowserWhenStdoutIsTerminal(t *testing.T) {
	h := okHarness(t)
	stdoutIsTTY = func() bool { return true }

	if code := h.run(nil, "content\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	want := h.srv.URL + "/x7kQ3f"
	if len(h.opened) != 1 || h.opened[0] != want {
		t.Errorf("opened = %v, want exactly [%s]", h.opened, want)
	}
}

func TestRunSkipsBrowserWhenStdoutIsPiped(t *testing.T) {
	h := okHarness(t)
	stdoutIsTTY = func() bool { return false }

	if code := h.run(nil, "content\n"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(h.opened) != 0 {
		t.Errorf("opened = %v, want none when piping", h.opened)
	}
}

func TestRunSkipsBrowserWithNoOpenFlag(t *testing.T) {
	h := okHarness(t)
	stdoutIsTTY = func() bool { return true }

	if code := h.run([]string{"--no-open"}, "content\n"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(h.opened) != 0 {
		t.Errorf("opened = %v, want none with --no-open", h.opened)
	}
}

func TestRunSucceedsEvenIfBrowserFailsToLaunch(t *testing.T) {
	h := okHarness(t)
	stdoutIsTTY = func() bool { return true }
	openInBrowser = func(string) error { return fmt.Errorf("no display") }

	// The upload already succeeded and the URL is already printed, so a browser
	// failure must not turn into a non-zero exit.
	if code := h.run(nil, "content\n"); code != 0 {
		t.Errorf("exit = %d, want 0 despite the browser failing", code)
	}
	if !strings.Contains(h.stdout.String(), "/x7kQ3f") {
		t.Errorf("stdout = %q, want the URL", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "no display") {
		t.Errorf("stderr = %q, want a warning about the browser", h.stderr.String())
	}
}

func TestRunRefusesBinaryFileWithoutUploading(t *testing.T) {
	h := okHarness(t)
	path := writeFile(t, "image.png", "\x89PNG\r\n\x1a\n\x00\x00IHDR")

	if code := h.run([]string{path}, ""); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "binary") {
		t.Errorf("stderr = %q, want a binary-file refusal", h.stderr.String())
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0: validation must precede the network", len(h.requests))
	}
}

func TestRunRefusesEmptyFileWithoutUploading(t *testing.T) {
	h := okHarness(t)
	path := writeFile(t, "empty.md", "   \n\t\n")

	if code := h.run([]string{path}, ""); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "empty") {
		t.Errorf("stderr = %q, want an empty-content error", h.stderr.String())
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestRunRefusesOversizeFileWithoutUploading(t *testing.T) {
	h := okHarness(t)
	path := writeFile(t, "huge.md", strings.Repeat("x", maxContentBytes+1))

	if code := h.run([]string{path}, ""); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "too large") {
		t.Errorf("stderr = %q, want a size error", h.stderr.String())
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestRunReportsMissingFile(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"/no/such/file.md"}, ""); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "/no/such/file.md") {
		t.Errorf("stderr = %q, want it to name the missing file", h.stderr.String())
	}
}

func TestRunPrintsUsageWhenNoArgsAndStdinIsTerminal(t *testing.T) {
	h := okHarness(t)
	stdinIsTTY = func() bool { return true }

	// Without this, a bare `yeet` would silently block on stdin forever.
	if code := h.run(nil, ""); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(h.stderr.String(), "usage") {
		t.Errorf("stderr = %q, want usage text", h.stderr.String())
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestRunRejectsMultipleFiles(t *testing.T) {
	h := okHarness(t)
	a := writeFile(t, "a.md", "a")
	b := writeFile(t, "b.md", "b")

	if code := h.run([]string{a, b}, ""); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestRunRejectsUnknownType(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"--type", "yaml"}, "x\n"); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(h.stderr.String(), "yaml") {
		t.Errorf("stderr = %q, want it to name the bad type", h.stderr.String())
	}
}

func TestRunRejectsInvalidExpiry(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"--expire", "tomorrow"}, "x\n"); code != 2 {
		t.Errorf("exit = %d, want 2 (usage)", code)
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestRunReportsServerError(t *testing.T) {
	h := newHarness(t, http.StatusBadRequest, `{"error":"Content is required"}`)

	if code := h.run(nil, "x\n"); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "Content is required") {
		t.Errorf("stderr = %q, want the server message", h.stderr.String())
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing printed on failure", h.stdout.String())
	}
}

func TestRunVersionFlag(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"--version"}, ""); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(h.stdout.String(), version) {
		t.Errorf("stdout = %q, want it to contain %q", h.stdout.String(), version)
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestRunHelpFlagExitsZero(t *testing.T) {
	h := okHarness(t)

	if code := h.run([]string{"--help"}, ""); code != 0 {
		t.Errorf("exit = %d, want 0 for an explicit --help", code)
	}
}

func TestRunUsesPasswordFromEnvironmentWithoutPrompting(t *testing.T) {
	h := okHarness(t)
	t.Setenv("YEET_PASSWORD", "fromenv")
	// readPassword is stubbed to fail, so a prompt would break this test.

	if code := h.run([]string{"--password"}, "x\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if body := h.body(t, 0); body["password"] != "fromenv" {
		t.Errorf("password = %v, want fromenv", body["password"])
	}
}

func TestRunPromptsForPasswordWhenFlagGivenAndEnvEmpty(t *testing.T) {
	h := okHarness(t)
	readPassword = func(prompt string) (string, error) { return "typed-secret", nil }

	if code := h.run([]string{"--password"}, "x\n"); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if body := h.body(t, 0); body["password"] != "typed-secret" {
		t.Errorf("password = %v, want typed-secret", body["password"])
	}
}

func TestRunOmitsPasswordWhenFlagNotGiven(t *testing.T) {
	h := okHarness(t)
	t.Setenv("YEET_PASSWORD", "ignored-without-flag")

	if code := h.run(nil, "x\n"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, present := h.body(t, 0)["password"]; present {
		t.Error("password was sent without --password; the env var alone must not enable it")
	}
}

func TestRunReportsPasswordPromptFailure(t *testing.T) {
	h := okHarness(t)
	readPassword = func(prompt string) (string, error) {
		return "", fmt.Errorf("not a terminal")
	}

	if code := h.run([]string{"--password"}, "x\n"); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0: nothing should upload without the password", len(h.requests))
	}
}

func TestRunRejectsEmptyPassword(t *testing.T) {
	h := okHarness(t)
	readPassword = func(prompt string) (string, error) { return "", nil }

	if code := h.run([]string{"--password"}, "x\n"); code != 1 {
		t.Errorf("exit = %d, want 1 for an empty password", code)
	}
	if len(h.requests) != 0 {
		t.Errorf("sent %d requests, want 0", len(h.requests))
	}
}

func TestResolveBaseURL(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"unset falls back to the public instance", "", defaultBaseURL},
		{"env override", "http://localhost:5173", "http://localhost:5173"},
		{"trailing slash trimmed", "http://localhost:5173/", "http://localhost:5173"},
		{"surrounding whitespace trimmed", "  http://localhost:5173  ", "http://localhost:5173"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("YEET_URL", tt.env)
			if got := resolveBaseURL(); got != tt.want {
				t.Errorf("resolveBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
