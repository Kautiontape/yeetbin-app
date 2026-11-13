package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveTarget(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }

	cases := []struct {
		name          string
		local, public bool
		env           string
		installed     func() bool
		want          string
		wantErr       bool
	}{
		{"auto, not installed", false, false, "", no, targetPublic, false},
		{"auto, installed", false, false, "", yes, targetLocal, false},
		{"explicit auto, installed", false, false, "auto", yes, targetLocal, false},
		{"env is case and space tolerant", false, false, " Local ", no, targetLocal, false},
		{"env local, not installed", false, false, "local", no, targetLocal, false},
		{"env public, installed", false, false, "public", yes, targetPublic, false},
		{"--local beats env public", true, false, "public", no, targetLocal, false},
		{"--public beats env local", false, true, "local", yes, targetPublic, false},
		{"--public beats installed", false, true, "", yes, targetPublic, false},
		{"--local without install", true, false, "", no, targetLocal, false},
		{"both flags", true, true, "", no, "", true},
		{"unknown env", false, false, "moon", no, "", true},
	}
	for _, c := range cases {
		got, err := resolveTarget(c.local, c.public, c.env, c.installed)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("%s: target = %q, want %q", c.name, got, c.want)
		}
	}
}

// Marks the native host as installed in the harness's temporary config home.
func installManifest(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "vivaldi", "NativeMessagingHosts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hostName+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Accepts one CLI request on path, reports it, and answers with resp.
func fakeHost(t *testing.T, path string, resp localResponse) <-chan localRequest {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	got := make(chan localRequest, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req localRequest
		json.NewDecoder(conn).Decode(&req)
		got <- req
		json.NewEncoder(conn).Encode(resp)
	}()
	return got
}

func received(t *testing.T, ch <-chan localRequest) localRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("the local host never received a request")
		return localRequest{}
	}
}

const localURL = "chrome-extension://" + extensionID + "/index.html#/b/abc"

func TestRunLocalSendsToHostAndPrintsURL(t *testing.T) {
	h := okHarness(t)
	stdoutIsTTY = func() bool { return true }
	got := fakeHost(t, h.socket, localResponse{ID: "abc", URL: localURL})
	path := writeFile(t, "notes.md", "# Hello\n")

	if code := h.run([]string{"--local", path}, ""); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, h.stderr.String())
	}
	if h.stdout.String() != localURL+"\n" {
		t.Errorf("stdout = %q, want %q", h.stdout.String(), localURL+"\n")
	}

	req := received(t, got)
	if req.Content != "# Hello\n" || req.Type != "markdown" || req.Title != "notes.md" || !req.Open {
		t.Errorf("request = %+v", req)
	}
	if len(h.requests) != 0 {
		t.Errorf("public server got %d requests, want none", len(h.requests))
	}
	if len(h.opened) != 0 {
		t.Errorf("opened %v; the extension opens local tabs, not xdg-open", h.opened)
	}
}

func TestRunLocalOpenFollowsTerminalAndNoOpen(t *testing.T) {
	cases := []struct {
		name   string
		tty    bool
		noOpen bool
		want   bool
	}{
		{"terminal", true, false, true},
		{"piped", false, false, false},
		{"--no-open", true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := okHarness(t)
			stdoutIsTTY = func() bool { return c.tty }
			got := fakeHost(t, h.socket, localResponse{ID: "abc", URL: localURL})

			args := []string{"--local"}
			if c.noOpen {
				args = append(args, "--no-open")
			}
			if code := h.run(args, "hi\n"); code != 0 {
				t.Fatalf("exit = %d (stderr: %s)", code, h.stderr.String())
			}
			if req := received(t, got); req.Open != c.want {
				t.Errorf("open = %v, want %v", req.Open, c.want)
			}
		})
	}
}

func TestRunLocalStdinHasNoTitleAndKeepsLanguage(t *testing.T) {
	h := okHarness(t)
	got := fakeHost(t, h.socket, localResponse{ID: "abc", URL: localURL})

	if code := h.run([]string{"--local", "--lang", "go"}, "package main\n"); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, h.stderr.String())
	}
	req := received(t, got)
	if req.Title != "" || req.Type != "code" || req.Language != "go" {
		t.Errorf("request = %+v, want no title and code/go", req)
	}
}

func TestRunAutoGoesLocalWhenInstalled(t *testing.T) {
	h := okHarness(t)
	installManifest(t)
	got := fakeHost(t, h.socket, localResponse{ID: "abc", URL: localURL})

	if code := h.run(nil, "private notes\n"); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, h.stderr.String())
	}
	if req := received(t, got); req.Content != "private notes\n" {
		t.Errorf("content = %q", req.Content)
	}
	if len(h.requests) != 0 {
		t.Errorf("public server got %d requests, want none", len(h.requests))
	}
}

func TestRunAutoGoesPublicWhenNotInstalled(t *testing.T) {
	h := okHarness(t)

	if code := h.run(nil, "hello\n"); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, h.stderr.String())
	}
	if len(h.requests) != 1 {
		t.Errorf("public server got %d requests, want 1", len(h.requests))
	}
}

func TestRunPublicOverridesInstalledLocal(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		env  string
	}{
		"--public flag":      {args: []string{"--public"}},
		"YEET_TARGET=public": {env: "public"},
	} {
		t.Run(name, func(t *testing.T) {
			h := okHarness(t)
			t.Setenv("YEET_TARGET", c.env)
			installManifest(t)
			if code := h.run(c.args, "share me\n"); code != 0 {
				t.Fatalf("exit = %d (stderr: %s)", code, h.stderr.String())
			}
			if len(h.requests) != 1 {
				t.Errorf("public server got %d requests, want 1", len(h.requests))
			}
		})
	}
}

// The reason local exists is to keep content off the internet, so an unreachable
// local yeetbin must never turn into a public upload.
func TestRunUnreachableLocalNeverUploads(t *testing.T) {
	cases := map[string]struct {
		args      []string
		env       string
		installed bool
	}{
		"--local":               {args: []string{"--local"}},
		"YEET_TARGET=local":     {env: "local"},
		"auto with host set up": {installed: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := okHarness(t)
			t.Setenv("YEET_TARGET", c.env)
			if c.installed {
				installManifest(t)
			}

			if code := h.run(c.args, "secret\n"); code != exitError {
				t.Fatalf("exit = %d, want %d", code, exitError)
			}
			if len(h.requests) != 0 {
				t.Fatalf("public server got %d requests, want none", len(h.requests))
			}
			if !strings.Contains(h.stderr.String(), "isn't running") ||
				!strings.Contains(h.stderr.String(), "--public") {
				t.Errorf("stderr = %q, want a hint to start the browser or pass --public", h.stderr.String())
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", h.stdout.String())
			}
		})
	}
}

func TestRunLocalRejectsPublicOnlyFlags(t *testing.T) {
	flags := map[string][]string{
		"--burn":     {"--burn"},
		"--expire":   {"--expire", "1h"},
		"--password": {"--password"},
	}
	for name, extra := range flags {
		for _, mode := range []string{"--local", "auto-installed"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				h := okHarness(t)
				t.Setenv("YEET_PASSWORD", "hunter2")
				args := extra
				if mode == "--local" {
					args = append([]string{"--local"}, extra...)
				} else {
					installManifest(t)
				}

				if code := h.run(args, "secret\n"); code != exitUsage {
					t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitUsage, h.stderr.String())
				}
				if len(h.requests) != 0 {
					t.Errorf("public server got %d requests, want none", len(h.requests))
				}
				if !strings.Contains(h.stderr.String(), name) || !strings.Contains(h.stderr.String(), "--public") {
					t.Errorf("stderr = %q, want it to name %s and suggest --public", h.stderr.String(), name)
				}
			})
		}
	}
}

func TestRunRejectsLocalAndPublicTogether(t *testing.T) {
	h := okHarness(t)
	if code := h.run([]string{"--local", "--public"}, "x\n"); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if len(h.requests) != 0 {
		t.Errorf("public server got %d requests, want none", len(h.requests))
	}
}

func TestRunRejectsUnknownTarget(t *testing.T) {
	h := okHarness(t)
	t.Setenv("YEET_TARGET", "moon")
	if code := h.run(nil, "x\n"); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(h.stderr.String(), "YEET_TARGET") {
		t.Errorf("stderr = %q, want it to name YEET_TARGET", h.stderr.String())
	}
}

func TestRunLocalReportsExtensionError(t *testing.T) {
	h := okHarness(t)
	fakeHost(t, h.socket, localResponse{Error: "storage is full"})

	if code := h.run([]string{"--local"}, "x\n"); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(h.stderr.String(), "storage is full") {
		t.Errorf("stderr = %q, want the extension's error", h.stderr.String())
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", h.stdout.String())
	}
}
