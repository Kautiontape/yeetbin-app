# yeet — CLI client for yeetbin

## Purpose

Upload a file or stdin to a [yeetbin](https://yeet.kautiontape.com) instance and get a
shareable URL back. Optimised for the one-liner: `yeet notes.md`.

## Constraints

- Go, stdlib only. One static binary, no runtime dependencies.
- Small enough to read in one sitting. Every non-trivial behaviour has a test.
- No test touches the real network or opens a browser.

## Server contract

`POST {base}/api/bin` with a JSON body:

| Field | Notes |
|---|---|
| `content` | required, non-empty after trim (server 400s otherwise) |
| `type` | `markdown` \| `code` \| `mermaid` \| `text`; server defaults to `markdown` |
| `language` | code language, only meaningful when `type: code` |
| `burn` | delete after first view |
| `expires_at` | RFC3339 timestamp, or omitted for permanent |
| `password` | plaintext; server bcrypt-hashes it |

Returns `201 {"id": "x7kQ3f", "url": "/x7kQ3f"}`. Note `url` is **relative** and must be
joined onto the base URL. Errors return `{"error": "..."}` — but a proxy failure returns an
HTML body, so response parsing must not assume JSON.

`mode` and `encrypted` are left at their server defaults (`read-only`, `false`). Client-side
encryption is deliberately out of scope; see Non-goals.

## CLI surface

```
yeet [flags] [file]        # file argument
yeet [flags] -             # explicit stdin
cat notes.md | yeet        # implicit stdin (no arg, stdin is not a TTY)
yeet                       # no arg, stdin IS a TTY → print usage, exit 2
```

| Flag | Behaviour |
|---|---|
| `--type` | Force content type. Rejects unknown values. |
| `--lang` | Force code language. Implies `--type code`. |
| `--expire` | `1h`, `24h`, `7d`, `30d`, any Go duration, or `Nd`. Must be positive. |
| `--burn` | Delete after first view. |
| `--password` | Prompt with echo disabled. Falls back to `YEET_PASSWORD`. |
| `--no-open` | Never open a browser. |
| `--version` | Print version, exit 0. |

Environment: `YEET_URL` (default `https://yeet.kautiontape.com`, trailing slash trimmed),
`YEET_PASSWORD` (non-interactive password).

Exit codes: `0` success, `1` error, `2` usage error.

## Type detection

Chosen from the file extension, case-insensitively. Stdin has no filename, so it defaults to
`markdown`.

- `markdown` — `.md` `.markdown` `.mdown` `.mkd`
- `mermaid` — `.mmd` `.mermaid`
- `text` — `.txt` `.log`, **and every unrecognised extension**
- `code` — the extension map below

The server preloads 24 shiki languages and falls back to plain text for anything else
(`src/lib/server/plugins/shiki.ts`), so an imperfect language guess degrades gracefully
rather than erroring. The map targets that preloaded set:

| Language | Extensions |
|---|---|
| `javascript` | `.js` `.mjs` `.cjs` `.jsx` |
| `typescript` | `.ts` `.tsx` |
| `python` | `.py` `.pyw` |
| `go` | `.go` |
| `rust` | `.rs` |
| `ruby` | `.rb` |
| `java` | `.java` |
| `c` | `.c` `.h` |
| `cpp` | `.cpp` `.cc` `.cxx` `.hpp` `.hh` |
| `csharp` | `.cs` |
| `php` | `.php` |
| `lua` | `.lua` |
| `bash` | `.sh` `.bash` `.zsh` |
| `sql` | `.sql` |
| `yaml` | `.yaml` `.yml` |
| `toml` | `.toml` |
| `json` | `.json` |
| `html` | `.html` `.htm` |
| `css` | `.css` |
| `diff` | `.diff` `.patch` |
| `dockerfile` | `.dockerfile`, or a file literally named `Dockerfile` |

## Content validation

"Does the sizing and type make sense" — all three checks run before any network call:

1. **Empty** — nothing but whitespace is rejected, matching the server's own rule.
2. **Too large** — over 2 MiB. The error reports the actual size.
3. **Binary** — a NUL byte, or invalid UTF-8. yeetbin renders text; a JPEG would produce a
   useless bin. Rejected with `refusing to yeet what looks like a binary file`.

## Structure

Flat `package main`, four source files. `main()` is a single line delegating to
`run(args, stdin, stdout, stderr) int`, which is what makes the CLI testable end to end.

| File | Responsibility |
|---|---|
| `main.go` | Flag parsing, input resolution, wiring, exit codes |
| `detect.go` | Extension map and content validation. Pure functions, no I/O. |
| `client.go` | API contract, POST, URL joining, error mapping |
| `terminal.go` | The only environment-touching code: TTY detection, no-echo password prompt, browser open. Exposed as function vars so tests substitute them. |

No-echo input is not in the stdlib, so the prompt shells out to `stty -echo`, restoring the
terminal state on completion.

## Output

The URL always goes to stdout on its own line, so `yeet f.md | xclip` works. The browser is
opened only when stdout is a TTY and `--no-open` was not passed — piping therefore never
spawns a browser. A browser that fails to launch is a warning on stderr, not a failure; the
upload already succeeded and the URL is already printed.

## Testing

| File | Covers |
|---|---|
| `detect_test.go` | Extension table, case-insensitivity, `Dockerfile`, unknown → `text`, stdin default; all three validation refusals including a boundary at exactly 2 MiB |
| `client_test.go` | Exact JSON body per flag combination, omitted optional fields, relative-URL joining, 201, 400 with `{error}`, 502 with HTML, connection refused |
| `main_test.go` | `run()` end to end against `httptest`: file and stdin paths, flag overrides, expiry parsing, usage errors, exit codes, browser suppression when piped |

## Non-goals

- Client-side encryption (AES-GCM, key in URL fragment). Real work, and `--password` already
  covers casual access control. `--password` is **not** encryption: content reaches the
  server in plaintext.
- `mode` selection (editable / forkable), fork, update, delete.
- Retrieving or listing existing bins. This tool only creates.
