// Command yeet uploads a file or stdin to a yeetbin instance and prints the resulting
// shareable URL.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// Stamped by packaging via -ldflags "-X main.version=...".
var version = "0.1.0"

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

Uploads a file, or stdin when no file is given, and prints the shareable URL.

  yeet notes.md              upload a file
  cat notes.md | yeet        upload stdin
  yeet - < notes.md          upload stdin explicitly

flags:
`

func main() {
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

	var (
		data []byte
		err  error
	)
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
