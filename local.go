package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The extension depends on both of these exactly: the manifest "key" pins the ID.
const (
	hostName        = "com.kautiontape.yeetbin"
	extensionID     = "dbkggnlfieppdhhcblaegcohnenmilbd"
	extensionOrigin = "chrome-extension://" + extensionID + "/"
)

const (
	targetAuto   = "auto"
	targetLocal  = "local"
	targetPublic = "public"
)

const (
	localDialTimeout    = time.Second
	localRequestTimeout = 25 * time.Second
)

// Variables so tests can substitute them.
var (
	socketPath = socketPathReal
	dialLocal  = func(path string) (net.Conn, error) {
		return net.DialTimeout("unix", path, localDialTimeout)
	}
	configHome = configHomeReal
)

var errLocalUnreachable = errors.New(
	"local yeetbin isn't running: open Vivaldi with the yeetbin extension, or pass --public")

// Sent by the CLI to the native host over the socket.
type localRequest struct {
	Content  string `json:"content"`
	Type     string `json:"type"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Open     bool   `json:"open"`
}

// The host's reply: either ID and URL, or Error.
type localResponse struct {
	ID    string `json:"id,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

func socketPathReal() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "yeet", "local.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("yeet-%d", os.Getuid()), "local.sock")
}

func configHomeReal() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find your config directory: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}

// Chromium-family config dirs, relative to the XDG config home, that read
// NativeMessagingHosts/ manifests.
var browserConfigDirs = []string{
	"vivaldi",
	"vivaldi-snapshot",
	"chromium",
	"google-chrome",
	"BraveSoftware/Brave-Browser",
}

func manifestPaths(root string) []string {
	paths := make([]string, len(browserConfigDirs))
	for i, b := range browserConfigDirs {
		paths[i] = filepath.Join(root, b, "NativeMessagingHosts", hostName+".json")
	}
	return paths
}

// Installed means a host manifest is registered with at least one browser, which is
// the user's opt-in to keeping yeets local.
func localInstalled() bool {
	root, err := configHome()
	if err != nil {
		return false
	}
	for _, p := range manifestPaths(root) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// Flags beat YEET_TARGET; auto picks local only once the bridge is installed.
func resolveTarget(localFlag, publicFlag bool, env string, installed func() bool) (string, error) {
	if localFlag && publicFlag {
		return "", fmt.Errorf("--local and --public are mutually exclusive")
	}
	if localFlag {
		return targetLocal, nil
	}
	if publicFlag {
		return targetPublic, nil
	}

	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", targetAuto:
		if installed() {
			return targetLocal, nil
		}
		return targetPublic, nil
	case targetLocal:
		return targetLocal, nil
	case targetPublic:
		return targetPublic, nil
	default:
		return "", fmt.Errorf("invalid YEET_TARGET %q (want auto, local or public)", env)
	}
}

// Never falls back to the public instance: an unreachable socket is an error.
func sendLocal(path string, req localRequest) (string, error) {
	conn, err := dialLocal(path)
	if err != nil {
		return "", errLocalUnreachable
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(localRequestTimeout))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return "", fmt.Errorf("sending to local yeetbin: %w", err)
	}

	var resp localResponse
	if err := json.NewDecoder(io.LimitReader(conn, 64<<10)).Decode(&resp); err != nil {
		return "", fmt.Errorf("no reply from local yeetbin: %w", err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("local yeetbin: %s", resp.Error)
	}
	if resp.URL == "" {
		return "", fmt.Errorf("local yeetbin replied without a URL")
	}
	return resp.URL, nil
}
