package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A variable so tests can substitute it; the test binary lives in a go-build dir.
var executablePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

type hostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// Registers this binary with every Chromium-family browser that has a config dir.
func installNativeHost(stdout, stderr io.Writer) int {
	exe, err := executablePath()
	if err != nil {
		fmt.Fprintf(stderr, "yeet: cannot find the yeet binary: %v\n", err)
		return exitError
	}
	// A `go run` binary is deleted on exit, which would leave a dangling registration.
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		fmt.Fprintf(stderr, "yeet: %s is a temporary go run binary; build or install yeet, then run this from the installed copy\n", exe)
		return exitError
	}

	root, err := configHome()
	if err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitError
	}

	data, err := json.MarshalIndent(hostManifest{
		Name:           hostName,
		Description:    "yeet CLI bridge to the local yeetbin extension",
		Path:           exe,
		Type:           "stdio",
		AllowedOrigins: []string{extensionOrigin},
	}, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitError
	}
	data = append(data, '\n')

	written := 0
	for i, path := range manifestPaths(root) {
		browserDir := filepath.Join(root, browserConfigDirs[i])
		if info, err := os.Stat(browserDir); err != nil || !info.IsDir() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(stderr, "yeet: %v\n", err)
			return exitError
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintf(stderr, "yeet: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "wrote %s\n", path)
		written++
	}

	if written == 0 {
		fmt.Fprintf(stderr, "yeet: no supported browser found under %s (looked for %s)\n",
			root, strings.Join(browserConfigDirs, ", "))
		return exitError
	}
	fmt.Fprintln(stdout, "Next: load the yeetbin extension in your browser. From then on `yeet` keeps content local; pass --public to share.")
	return exitOK
}

func uninstallNativeHost(stdout, stderr io.Writer) int {
	root, err := configHome()
	if err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitError
	}

	removed := 0
	for _, path := range manifestPaths(root) {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "yeet: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "removed %s\n", path)
		removed++
	}
	if removed == 0 {
		fmt.Fprintln(stdout, "nothing to remove: the native host was not installed")
	}
	return exitOK
}
