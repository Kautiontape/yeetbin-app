package main

import (
	"strings"
	"testing"
)

func TestDetectType(t *testing.T) {
	tests := []struct {
		filename string
		wantType string
		wantLang string
	}{
		// Markdown
		{"notes.md", "markdown", ""},
		{"README.markdown", "markdown", ""},
		{"a.mdown", "markdown", ""},
		{"a.mkd", "markdown", ""},

		// Mermaid
		{"flow.mmd", "mermaid", ""},
		{"flow.mermaid", "mermaid", ""},

		// Code, spot-checking the map
		{"main.go", "code", "go"},
		{"lib.rs", "code", "rust"},
		{"app.py", "code", "python"},
		{"index.js", "code", "javascript"},
		{"index.mjs", "code", "javascript"},
		{"component.jsx", "code", "javascript"},
		{"types.ts", "code", "typescript"},
		{"view.tsx", "code", "typescript"},
		{"main.c", "code", "c"},
		{"header.h", "code", "c"},
		{"engine.cpp", "code", "cpp"},
		{"engine.hpp", "code", "cpp"},
		{"Program.cs", "code", "csharp"},
		{"script.sh", "code", "bash"},
		{"profile.zsh", "code", "bash"},
		{"query.sql", "code", "sql"},
		{"config.yml", "code", "yaml"},
		{"config.yaml", "code", "yaml"},
		{"Cargo.toml", "code", "toml"},
		{"data.json", "code", "json"},
		{"page.html", "code", "html"},
		{"style.css", "code", "css"},
		{"fix.patch", "code", "diff"},

		// Dockerfile is matched by name, not extension
		{"Dockerfile", "code", "dockerfile"},
		{"/srv/app/Dockerfile", "code", "dockerfile"},

		// Explicit text
		{"out.txt", "text", ""},
		{"server.log", "text", ""},

		// Unknown extensions fall back to text, never to markdown
		{"data.xyz", "text", ""},
		{"LICENSE", "text", ""},
		{"noext", "text", ""},

		// Case insensitivity
		{"NOTES.MD", "markdown", ""},
		{"MAIN.GO", "code", "go"},

		// Paths are stripped before matching
		{"/home/shawn/docs/notes.md", "markdown", ""},
		{"./rel/path/main.go", "code", "go"},

		// Dotted filenames use the final extension
		{"archive.tar.md", "markdown", ""},

		// Empty filename means stdin, which defaults to markdown
		{"", "markdown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			gotType, gotLang := detectType(tt.filename)
			if gotType != tt.wantType || gotLang != tt.wantLang {
				t.Errorf("detectType(%q) = (%q, %q), want (%q, %q)",
					tt.filename, gotType, gotLang, tt.wantType, tt.wantLang)
			}
		})
	}
}

func TestValidateContentRejectsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t\n", " \r\n "} {
		if err := validateContent([]byte(in)); err == nil {
			t.Errorf("validateContent(%q) = nil, want an error about empty content", in)
		} else if !strings.Contains(err.Error(), "empty") {
			t.Errorf("validateContent(%q) error = %q, want it to mention %q", in, err, "empty")
		}
	}
}

func TestValidateContentAcceptsOrdinaryText(t *testing.T) {
	if err := validateContent([]byte("# Hello\n\nsome *markdown*\n")); err != nil {
		t.Errorf("validateContent(ordinary markdown) = %v, want nil", err)
	}
}

func TestValidateContentRejectsOversize(t *testing.T) {
	err := validateContent([]byte(strings.Repeat("a", maxContentBytes+1)))
	if err == nil {
		t.Fatalf("validateContent(maxContentBytes+1) = nil, want a size error")
	}
	// The message must report the actual size so the user knows how far over they are.
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want it to mention %q", err, "too large")
	}
}

func TestValidateContentAcceptsExactlyMaxSize(t *testing.T) {
	if err := validateContent([]byte(strings.Repeat("a", maxContentBytes))); err != nil {
		t.Errorf("validateContent(exactly maxContentBytes) = %v, want nil (boundary is inclusive)", err)
	}
}

func TestValidateContentRejectsBinary(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"NUL byte", []byte("text\x00more text")},
		{"invalid UTF-8", []byte{'h', 'i', 0xff, 0xfe}},
		{"PNG header", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateContent(tt.data)
			if err == nil {
				t.Fatalf("validateContent(%s) = nil, want a binary-file error", tt.name)
			}
			if !strings.Contains(err.Error(), "binary") {
				t.Errorf("error = %q, want it to mention %q", err, "binary")
			}
		})
	}
}
