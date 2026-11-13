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

Arch Linux, from the [ktn] repo:

```sh
sudo pacman -S yeetbin-app
```

The package is built and published by
[Kautiontape/ktn-repo](https://github.com/Kautiontape/ktn-repo), which holds the PKGBUILD.
It installs `/usr/bin/yeet` and conflicts with the unrelated
[`yeet`](https://aur.archlinux.org/packages/yeet) pacman wrapper.

With Go:

```sh
go install github.com/Kautiontape/yeetbin-app@latest
```

From a checkout:

```sh
go build -trimpath -ldflags="-s -w" -o ~/.local/bin/yeet .
```

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
| `--local` | Keep it in the [local yeetbin](#local-yeetbin); fail if it isn't running |
| `--public` | Upload to the public instance, even with local yeetbin installed |
| `--install-native-host` | Register `yeet` with your browsers for local yeetbin |
| `--uninstall-native-host` | Remove that registration |
| `--version` | Print the version |

| Environment | |
|---|---|
| `YEET_TARGET` | `auto` (default), `local` or `public`. See [Local yeetbin](#local-yeetbin). |
| `YEET_URL` | Public instance to upload to. Defaults to `https://yeet.kautiontape.com`. |
| `YEET_PASSWORD` | Supplies the password non-interactively, for use with `--password`. |

`--password` is server-side access control, not encryption: the content reaches the server
in plaintext. For zero-knowledge encryption, use the web UI.

## Local yeetbin

The yeetbin browser extension renders yeets locally, with the same look as the server,
and nothing leaves your machine until you press Publish in it. `yeet` hands content to the
extension through Chrome native messaging: the browser starts `yeet` as a helper, and that
helper listens on a socket in `$XDG_RUNTIME_DIR/yeet/`.

Set it up once, on Linux with Vivaldi, Chromium, Chrome or Brave:

1. Build and load the extension, which lives in the server repo under
   [`extension/`](https://github.com/Kautiontape/yeetbin/tree/main/extension).
2. Run `yeet --install-native-host` from the installed binary (`/usr/bin/yeet` with the
   package). It writes `com.kautiontape.yeetbin.json` into each browser's
   `NativeMessagingHosts/` directory.

After that, `yeet notes.md` stores the content in the extension, opens it in a browser tab
and prints a `chrome-extension://` URL. That URL works only in your own browser. To get a
link you can share, pass `--public`.

Which instance gets the content:

| | Native host not installed | Installed |
|---|---|---|
| default (`YEET_TARGET=auto`) | public | local |
| `--local` or `YEET_TARGET=local` | local | local |
| `--public` or `YEET_TARGET=public` | public | public |

If yeet picks local and the extension isn't reachable (say the browser is closed), `yeet`
exits with an error. It never quietly uploads to the public instance instead. `--burn`,
`--expire` and `--password` only mean something on the public server, so with a local
target they are usage errors.

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
or launch a browser are variables the tests replace. The native host tests run it over a
Unix socket in a temp dir, with pipes standing in for the browser. The socket path, config
home and executable path are variables too, so tests never see this machine's browsers or a
running host.

The design lives in [`docs/superpowers/specs`](docs/superpowers/specs/). Packaging lives in
[Kautiontape/ktn-repo](https://github.com/Kautiontape/ktn-repo).
