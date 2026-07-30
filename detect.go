package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxContentBytes caps what we are willing to upload. yeetbin stores text; anything
// this large is almost certainly a mistake. The server imposes no limit of its own,
// so this guard lives here.
const maxContentBytes = 2 << 20 // 2 MiB

// Content types understood by the server's registry.
const (
	typeMarkdown = "markdown"
	typeCode     = "code"
	typeMermaid  = "mermaid"
	typeText     = "text"
)

// validTypes gates the --type flag.
var validTypes = []string{typeMarkdown, typeCode, typeMermaid, typeText}

// markdownExts and mermaidExts get their own content type rather than a language.
var markdownExts = map[string]bool{
	".md": true, ".markdown": true, ".mdown": true, ".mkd": true,
}

var mermaidExts = map[string]bool{
	".mmd": true, ".mermaid": true,
}

// codeExts maps an extension to a shiki language. The server preloads a fixed set of
// languages and degrades to plain text for anything outside it, so these values are
// chosen to land inside that set.
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

// detectType picks a content type from a filename. An empty filename means the content
// came from stdin, where there is no extension to go on, so it is treated as markdown.
// Unrecognised extensions become plain text rather than markdown, so that a file we
// don't understand is displayed verbatim instead of being mangled by a markdown renderer.
func detectType(filename string) (contentType, language string) {
	if filename == "" {
		return typeMarkdown, ""
	}

	base := strings.ToLower(filepath.Base(filename))

	// Dockerfile carries its type in the name, not an extension.
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

// validateContent answers whether the sizing and type make sense to upload, before we
// spend a network round trip finding out.
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

// mib formats a byte count in MiB. Only sizes at or above the limit are ever reported,
// so smaller units would be dead code.
func mib(n int) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
