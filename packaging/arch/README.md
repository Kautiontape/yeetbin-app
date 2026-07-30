# The [ktn-yeetbin] pacman repository

A signed, self-contained pacman repository published to the `ktn-repo` release tag of this
GitHub repo by `.github/workflows/ktn-repo.yml`. Installing from it needs no AUR account and
no Go toolchain.

## One database per project

Each project publishes its own repository:

| pacman section | Published from | Package |
|---|---|---|
| `[ktn-trilium]` | `Kautiontape/trilium` | `triliumnext-ktn-bin` |
| `[ktn-yeetbin]` | `Kautiontape/yeetbin-app` | `yeetbin-app` |

This split is deliberate. `repo-add` rewrites the whole database, and the publishing
workflows live in different repos, so GitHub's `concurrency` groups cannot serialise them.
Sharing one database would mean whichever workflow ran last published an index containing
only its own package, silently evicting the other. Separate databases make that impossible,
at the cost of one extra section in `pacman.conf`.

## Client setup

Add to `/etc/pacman.conf`, after the `[extra]` section:

```ini
[ktn-yeetbin]
SigLevel = Required
Server = https://github.com/Kautiontape/yeetbin-app/releases/download/ktn-repo
```

`SigLevel = Required` requires valid signatures on both the packages and the database, so
the signing key must be trusted by pacman:

```sh
sudo pacman-key --recv-keys C20F8574816A1B67C81E6F829DA4E0459723DB07
sudo pacman-key --lsign-key C20F8574816A1B67C81E6F829DA4E0459723DB07
```

Then:

```sh
sudo pacman -Sy yeetbin-app
yeet --version
```

`yeetbin-app` installs `/usr/bin/yeet`. If you previously built it by hand, delete
`~/.local/bin/yeet` — it precedes `/usr/bin` on `PATH` and would shadow the package.

## Publishing

Requires two repository secrets, holding the same key that signs `[ktn-trilium]`
(`Shawn Squire (ktn repo)`, `C20F8574816A1B67C81E6F829DA4E0459723DB07`). GitHub secrets are
write-only and cannot be copied between repos, so set them once here:

```sh
gpg --export-secret-keys --armor C20F8574816A1B67C81E6F829DA4E0459723DB07 \
  | gh secret set ARCH_REPO_GPG_KEY --repo Kautiontape/yeetbin-app

gh secret set ARCH_REPO_GPG_PASSPHRASE --repo Kautiontape/yeetbin-app
```

Publishing then happens automatically on any `v*` tag:

```sh
git tag -a v0.2.0 -m "yeet 0.2.0" && git push origin v0.2.0
```

Or on demand, which builds a `0.1.0.r12.gabc1234`-style dev version from the current commit:

```sh
gh workflow run "ktn repo" --repo Kautiontape/yeetbin-app
```

## Notes

- The package is built inside `archlinux:base-devel` rather than on the Ubuntu runner, so it
  links against Arch's glibc. Building on the runner would produce a binary tied to Ubuntu's
  glibc version.
- `options=('!debug')` matters more than it looks: without it, makepkg splits a Go binary's
  symbols into a second `yeetbin-app-debug` package, and the publish step can index the
  wrong one. The workflow additionally refuses to continue unless exactly one non-debug
  package was produced.
- `check()` runs `go test ./...` during the build, so a test regression fails the publish
  rather than shipping.
- The database is rebuilt from scratch each run. That is safe here precisely because nothing
  else writes to this repository.
