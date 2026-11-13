package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Points install at a temporary config home holding the given browser dirs.
func installEnv(t *testing.T, exe string, browsers ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	for _, b := range browsers {
		if err := os.MkdirAll(filepath.Join(root, b), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := executablePath
	t.Cleanup(func() { executablePath = orig })
	executablePath = func() (string, error) { return exe, nil }
	return root
}

func runInstall(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestInstallWritesManifestForEachBrowserPresent(t *testing.T) {
	root := installEnv(t, "/usr/bin/yeet", "vivaldi", "BraveSoftware/Brave-Browser")

	code, stdout, stderr := runInstall("--install-native-host")
	if code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}

	for _, b := range []string{"vivaldi", "BraveSoftware/Brave-Browser"} {
		path := filepath.Join(root, b, "NativeMessagingHosts", hostName+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		var m hostManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: invalid JSON: %v", b, err)
		}
		want := hostManifest{
			Name:           "com.kautiontape.yeetbin",
			Description:    m.Description,
			Path:           "/usr/bin/yeet",
			Type:           "stdio",
			AllowedOrigins: []string{"chrome-extension://dbkggnlfieppdhhcblaegcohnenmilbd/"},
		}
		if m.Name != want.Name || m.Path != want.Path || m.Type != want.Type ||
			len(m.AllowedOrigins) != 1 || m.AllowedOrigins[0] != want.AllowedOrigins[0] {
			t.Errorf("%s: manifest = %+v, want %+v", b, m, want)
		}
		if !strings.Contains(stdout, path) {
			t.Errorf("stdout does not mention %s:\n%s", path, stdout)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "chromium")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("created a dir for a browser that is not installed")
	}
	if !localInstalled() {
		t.Error("localInstalled() = false after install")
	}
}

func TestInstallFailsWhenNoBrowserIsPresent(t *testing.T) {
	installEnv(t, "/usr/bin/yeet")

	code, _, stderr := runInstall("--install-native-host")
	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "no supported browser") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestInstallRefusesTemporaryGoRunBinary(t *testing.T) {
	root := installEnv(t, "/tmp/go-build1234/b001/exe/yeet", "vivaldi")

	code, _, stderr := runInstall("--install-native-host")
	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "go run") {
		t.Errorf("stderr = %q, want a go run hint", stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "vivaldi", "NativeMessagingHosts")); !errors.Is(err, os.ErrNotExist) {
		t.Error("wrote a manifest pointing at a temporary binary")
	}
}

func TestUninstallRemovesManifests(t *testing.T) {
	installEnv(t, "/usr/bin/yeet", "vivaldi", "chromium")
	if code, _, stderr := runInstall("--install-native-host"); code != exitOK {
		t.Fatalf("install exit = %d (stderr: %s)", code, stderr)
	}

	code, stdout, stderr := runInstall("--uninstall-native-host")
	if code != exitOK {
		t.Fatalf("uninstall exit = %d (stderr: %s)", code, stderr)
	}
	if strings.Count(stdout, "removed ") != 2 {
		t.Errorf("stdout = %q, want two removals", stdout)
	}
	if localInstalled() {
		t.Error("localInstalled() = true after uninstall")
	}

	code, stdout, _ = runInstall("--uninstall-native-host")
	if code != exitOK || !strings.Contains(stdout, "nothing to remove") {
		t.Errorf("second uninstall: exit %d, stdout %q", code, stdout)
	}
}

func TestInstallAndUninstallAreMutuallyExclusive(t *testing.T) {
	installEnv(t, "/usr/bin/yeet", "vivaldi")
	if code, _, _ := runInstall("--install-native-host", "--uninstall-native-host"); code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
}
