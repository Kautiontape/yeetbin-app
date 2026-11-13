// Command yeet sends a file or stdin to a yeetbin instance, either the public server or
// the local yeetbin browser extension, and prints the resulting URL.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Stamped by packaging via -ldflags "-X main.version=...".
var version = "0.2.0"

const (
	defaultBaseURL = "https://yeet.kautiontape.com"
	requestTimeout = 30 * time.Second
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usageText = `usage: yeet [flags] [file]

Yeets a file, or stdin when no file is given, and prints the URL. Once the local
yeetbin extension is installed, yeets stay in your browser unless you pass --public.

  yeet notes.md              yeet a file
  cat notes.md | yeet        yeet stdin
  yeet - < notes.md          yeet stdin explicitly
  yeet --public notes.md     upload to the public instance for a shareable link

environment:
  YEET_TARGET     auto (default), local or public
  YEET_URL        public instance (default https://yeet.kautiontape.com)
  YEET_PASSWORD   password for --password, skipping the prompt

flags:
`

func main() {
	// A browser launches its native messaging host with the caller's origin as argv[1].
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		os.Exit(runNativeHost(os.Stdin, os.Stdout, os.Stderr, socketPath()))
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("yeet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usageText)
		fs.PrintDefaults()
	}

	var (
		typeFlag   = fs.String("type", "", "content type: "+strings.Join(validTypes, ", "))
		langFlag   = fs.String("lang", "", "code language (implies -type code)")
		expireFlag = fs.String("expire", "", "expire after a duration, e.g. 1h, 24h, 7d, 30d")
		burnFlag   = fs.Bool("burn", false, "delete the bin after it is viewed once")
		passFlag   = fs.Bool("password", false, "protect the bin with a password (prompts, or reads YEET_PASSWORD)")
		noOpenFlag = fs.Bool("no-open", false, "never open a browser")
		localFlag  = fs.Bool("local", false, "keep it in the local yeetbin extension; fails if it isn't running")
		publicFlag = fs.Bool("public", false, "upload to the public instance even when local is installed")
		installArg = fs.Bool("install-native-host", false, "register yeet with your browsers so the local yeetbin extension can reach it")
		removeArg  = fs.Bool("uninstall-native-host", false, "remove that registration")
		versionArg = fs.Bool("version", false, "print the version and exit")
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	if *versionArg {
		fmt.Fprintf(stdout, "yeet %s\n", version)
		return exitOK
	}
	if *installArg && *removeArg {
		fmt.Fprintln(stderr, "yeet: --install-native-host and --uninstall-native-host are mutually exclusive")
		return exitUsage
	}
	if *installArg {
		return installNativeHost(stdout, stderr)
	}
	if *removeArg {
		return uninstallNativeHost(stdout, stderr)
	}

	target, err := resolveTarget(*localFlag, *publicFlag, os.Getenv("YEET_TARGET"), localInstalled)
	if err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitUsage
	}
	if target == targetLocal {
		var publicOnly []string
		if *burnFlag {
			publicOnly = append(publicOnly, "--burn")
		}
		if *expireFlag != "" {
			publicOnly = append(publicOnly, "--expire")
		}
		if *passFlag {
			publicOnly = append(publicOnly, "--password")
		}
		if len(publicOnly) > 0 {
			fmt.Fprintf(stderr, "yeet: %s only applies to public yeets; add --public\n",
				strings.Join(publicOnly, ", "))
			return exitUsage
		}
	}

	rest := fs.Args()
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "yeet: expected at most one file, got %d\n", len(rest))
		fs.Usage()
		return exitUsage
	}

	filename := ""
	if len(rest) == 1 {
		filename = rest[0]
	}

	// A bare `yeet` would otherwise block on stdin forever.
	if filename == "" && stdinIsTTY() {
		fmt.Fprint(stderr, usageText)
		fs.PrintDefaults()
		return exitUsage
	}

	var data []byte
	if filename == "" || filename == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, maxContentBytes+1))
		filename = "" // no extension to detect from
	} else {
		data, err = os.ReadFile(filename)
	}
	if err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitError
	}

	if err := validateContent(data); err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitError
	}

	contentType, language := detectType(filename)
	if *langFlag != "" {
		contentType, language = typeCode, *langFlag
	}
	if *typeFlag != "" {
		if !slices.Contains(validTypes, *typeFlag) {
			fmt.Fprintf(stderr, "yeet: unknown type %q (want one of: %s)\n",
				*typeFlag, strings.Join(validTypes, ", "))
			return exitUsage
		}
		contentType = *typeFlag
	}
	if contentType != typeCode {
		language = ""
	}

	if target == targetLocal {
		title := ""
		if filename != "" {
			title = filepath.Base(filename)
		}
		url, err := sendLocal(socketPath(), localRequest{
			Content:  string(data),
			Type:     contentType,
			Language: language,
			Title:    title,
			// xdg-open cannot open chrome-extension:// URLs, so the extension opens the tab.
			Open: !*noOpenFlag && stdoutIsTTY(),
		})
		if err != nil {
			fmt.Fprintf(stderr, "yeet: %v\n", err)
			return exitError
		}
		fmt.Fprintln(stdout, url)
		return exitOK
	}

	req := binRequest{
		Content:  string(data),
		Type:     contentType,
		Language: language,
		Burn:     *burnFlag,
	}

	if *expireFlag != "" {
		d, err := parseExpiry(*expireFlag)
		if err != nil {
			fmt.Fprintf(stderr, "yeet: %v\n", err)
			return exitUsage
		}
		req.ExpiresAt = time.Now().UTC().Add(d).Format(time.RFC3339)
	}

	if *passFlag {
		pw := os.Getenv("YEET_PASSWORD")
		if pw == "" {
			pw, err = readPassword("Password: ")
			if err != nil {
				fmt.Fprintf(stderr, "yeet: %v\n", err)
				return exitError
			}
		}
		if pw == "" {
			fmt.Fprintln(stderr, "yeet: password was empty")
			return exitError
		}
		req.Password = pw
	}

	c := &client{
		baseURL: resolveBaseURL(),
		http:    &http.Client{Timeout: requestTimeout},
	}
	url, err := c.create(req)
	if err != nil {
		fmt.Fprintf(stderr, "yeet: %v\n", err)
		return exitError
	}

	// Unconditional so the URL can be piped.
	fmt.Fprintln(stdout, url)

	if !*noOpenFlag && stdoutIsTTY() {
		if err := openInBrowser(url); err != nil {
			// The upload already succeeded, so this is only a warning.
			fmt.Fprintf(stderr, "yeet: could not open a browser: %v\n", err)
		}
	}
	return exitOK
}

func resolveBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("YEET_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultBaseURL
}
