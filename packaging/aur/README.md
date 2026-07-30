# AUR packaging

The canonical copy of the `yeetbin-app` PKGBUILD. The AUR has its own git repo
(`ssh://aur@aur.archlinux.org/yeetbin-app.git`) containing only `PKGBUILD` and `.SRCINFO`;
this directory is the source of truth that gets copied there.

## One-time AUR setup

1. Register an account at <https://aur.archlinux.org/register>.
2. Add an SSH **public** key under *My Account* → *SSH Public Key*.
3. Point ssh at that key:

   ```
   # ~/.ssh/config
   Host aur.archlinux.org
     User aur
     IdentityFile ~/.ssh/aur
     IdentitiesOnly yes
   ```

4. Confirm it works — this should greet you by username rather than
   `Permission denied (publickey)`:

   ```sh
   ssh aur@aur.archlinux.org help
   ```

The AUR repo is created by your first push; there is no "new package" button.

## Releasing a new version

Cutting a release is a tag on the app repo followed by a checksum bump here.

```sh
# 1. In the repo root: tag and push. The version in the binary is stamped from
#    pkgver at build time, so there is nothing to edit in Go source.
git tag -a v0.2.0 -m "yeet 0.2.0" && git push origin v0.2.0

# 2. Here: bump pkgver, reset pkgrel to 1, refresh the checksum and .SRCINFO.
sed -i 's/^pkgver=.*/pkgver=0.2.0/; s/^pkgrel=.*/pkgrel=1/' PKGBUILD
updpkgsums
makepkg --printsrcinfo > .SRCINFO

# 3. Verify it builds and lints cleanly before publishing.
makepkg -f            # runs check() -> go test ./...
namcap PKGBUILD *.pkg.tar.zst

# 4. Publish to the AUR.
cd ~/downloads/aur/yeetbin-app
cp ~/documents/apps/yeetbin-app/packaging/aur/{PKGBUILD,.SRCINFO} .
git commit -am "yeetbin-app 0.2.0" && git push
```

`.SRCINFO` must be regenerated and committed whenever `PKGBUILD` changes — the AUR web
interface reads metadata from it, and a stale one makes the package look wrong even when it
builds fine.

Bump `pkgrel` instead of `pkgver` when only the packaging changed and the upstream version
did not.

## Notes

- The package has zero Go module dependencies, so `build()` needs no network access and
  works in a clean chroot without a `prepare()` step to pre-fetch modules.
- It installs `/usr/bin/yeet`, colliding with the unrelated
  [`yeet`](https://aur.archlinux.org/packages/yeet) pacman wrapper, hence
  `conflicts=('yeet')`. There is deliberately no `provides=('yeet')`: this package does not
  provide that package's functionality.
- `check()` runs the full test suite during the build. The tests never touch the network.
