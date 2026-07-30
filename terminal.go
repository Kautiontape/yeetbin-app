package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// These are the only functions that touch the terminal or launch a browser. They are
// variables so that tests can substitute them; nothing else in the program does I/O
// beyond reading the input file and talking to the server.
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

// openInBrowserReal launches the system browser without waiting for it to exit.
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
	// Detach from our stdio so a chatty launcher cannot pollute the URL on stdout.
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child in the background rather than leaving a zombie.
	go cmd.Wait()
	return nil
}

// readPasswordReal reads a line from the terminal with echo disabled. Go's standard
// library has no no-echo read, so this drives stty and restores the previous terminal
// state afterwards.
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
		// Restore the exact prior state, and drop a newline since the user's Enter
		// was never echoed.
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
