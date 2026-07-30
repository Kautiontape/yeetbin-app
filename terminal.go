package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Variables so tests can substitute them.
var (
	openInBrowser = openInBrowserReal
	stdoutIsTTY   = func() bool { return isTerminal(os.Stdout) }
	stdinIsTTY    = func() bool { return isTerminal(os.Stdin) }
	readPassword  = readPasswordReal
)

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func openInBrowserReal(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// Detached so a chatty launcher cannot pollute stdout.
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap
	return nil
}

// Drives stty because the stdlib has no no-echo read.
func readPasswordReal(prompt string) (string, error) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return "", fmt.Errorf("cannot prompt for a password (no terminal): set YEET_PASSWORD instead")
	}
	defer tty.Close()

	saved, err := sttyState(tty)
	if err != nil {
		return "", err
	}
	if err := stty(tty, "-echo"); err != nil {
		return "", err
	}
	defer func() {
		stty(tty, saved)
		fmt.Fprintln(os.Stderr)
	}()

	fmt.Fprint(os.Stderr, prompt)

	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func sttyState(tty *os.File) (string, error) {
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = tty
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cannot read terminal state: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func stty(tty *os.File, args ...string) error {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	return cmd.Run()
}
