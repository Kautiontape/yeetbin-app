package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The server imposes no limit of its own.
const maxContentBytes = 2 << 20 // 2 MiB

const (
	typeMarkdown = "markdown"
	typeCode     = "code"
	typeMermaid  = "mermaid"
	typeText     = "text"
)

var validTypes = []string{typeMarkdown, typeCode, typeMermaid, typeText}

var markdownExts = map[string]bool{
	".md": true, ".markdown": true, ".mdown": true, ".mkd": true,
}

var mermaidExts = map[string]bool{
	".mmd": true, ".mermaid": true,
}

// Values are shiki languages; the server degrades to plain text for unknown ones.
var codeExts = map[string]string{
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript",
	".py": "python", ".pyw": "python",
	".go":   "go",
	".rs":   "rust",
	".rb":   "ruby",
	".java": "java",
	".c":    "c", ".h": "c",
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
	".cs":  "csharp",
	".php": "php",
	".lua": "lua",
	".sh":  "bash", ".bash": "bash", ".zsh": "bash",
	".sql":  "sql",
	".yaml": "yaml", ".yml": "yaml",
	".toml": "toml",
	".json": "json",
	".html": "html", ".htm": "html",
	".css":  "css",
	".diff": "diff", ".patch": "diff",
	".dockerfile": "dockerfile",
}

// An empty filename means stdin. Unknown extensions become text, not markdown.
func detectType(filename string) (contentType, language string) {
	if filename == "" {
		return typeMarkdown, ""
	}

	base := strings.ToLower(filepath.Base(filename))

	if base == "dockerfile" {
		return typeCode, "dockerfile"
	}

	ext := filepath.Ext(base)
	switch {
	case markdownExts[ext]:
		return typeMarkdown, ""
	case mermaidExts[ext]:
		return typeMermaid, ""
	}
	if lang, ok := codeExts[ext]; ok {
		return typeCode, lang
	}
	return typeText, ""
}

func validateContent(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("nothing to yeet: content is empty")
	}
	if len(data) > maxContentBytes {
		return fmt.Errorf("content is too large: %s (limit is %s)",
			mib(len(data)), mib(maxContentBytes))
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return fmt.Errorf("refusing to yeet what looks like a binary file")
	}
	return nil
}

func mib(n int) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
