# yeetbin-app

Throw a file at [yeetbin](https://yeet.kautiontape.com) and get a link back. The command is
`yeet`.

```console
$ yeet notes.md
https://yeet.kautiontape.com/x7kQ3f
```

The URL is printed to stdout and opened in your browser. One static binary, Go stdlib only,
no runtime dependencies.

## Install

Arch Linux, from the AUR:

```sh
yay -S yeetbin-app
```

With Go:

```sh
go install github.com/Kautiontape/yeetbin-app@latest
```

From a checkout:

```sh
go build -trimpath -ldflags="-s -w" -o ~/.local/bin/yeet .
```

> [!note]
> The package installs `/usr/bin/yeet`, which collides with the unrelated
> [`yeet`](https://aur.archlinux.org/packages/yeet) pacman wrapper in the AUR. The two
> cannot be installed side by side, so `yeetbin-app` declares a conflict.

## Usage

```sh
yeet notes.md              # a file
cat notes.md | yeet        # stdin
yeet - < notes.md          # stdin, explicitly
yeet notes.md | xclip      # piped: prints the URL, skips the browser
```

| Flag | |
|---|---|
| `--type` | Force the content type: `markdown`, `code`, `mermaid`, `text` |
| `--lang` | Force the code language, implying `--type code` |
| `--expire` | Self-delete after `1h`, `24h`, `7d`, `30d`, any Go duration, or `Nd` |
| `--burn` | Delete after the first view |
| `--password` | Prompt for a password with echo off |
| `--no-open` | Never open a browser |
| `--version` | Print the version |

| Environment | |
|---|---|
| `YEET_URL` | Instance to upload to. Defaults to `https://yeet.kautiontape.com`. |
| `YEET_PASSWORD` | Supplies the password non-interactively, for use with `--password`. |

`--password` is server-side access control, not encryption: the content reaches the server
in plaintext. For zero-knowledge encryption, use the web UI.

## Type detection

The file extension picks the content type, so `yeet main.go` gets Go syntax highlighting
while `yeet notes.md` gets rendered markdown.

- **markdown** — `.md` `.markdown` `.mdown` `.mkd`
- **mermaid** — `.mmd` `.mermaid`
- **code** — `.go` `.rs` `.py` `.ts` `.js` `.rb` `.java` `.c` `.cpp` `.cs` `.php` `.lua`
  `.sh` `.sql` `.yml` `.toml` `.json` `.html` `.css` `.diff`, and `Dockerfile`
- **text** — `.txt`, `.log`, and anything unrecognised

Stdin has no filename, so it defaults to markdown. Override any guess with `--type` or
`--lang`.

## Refusals

Checked before anything is sent, so a mistake costs no round trip:

- empty or whitespace-only content
- larger than 2 MiB
- binary content — a NUL byte or invalid UTF-8

## Development

```sh
go test ./...          # unit and end-to-end tests
go test -race ./...
go vet ./...
```

Tests never touch the network or open a browser: `client_test.go` and `main_test.go` run
against `httptest` servers, and the four functions in `terminal.go` that touch the terminal
or launch a browser are variables the tests replace.

The design lives in [`docs/superpowers/specs`](docs/superpowers/specs/).
