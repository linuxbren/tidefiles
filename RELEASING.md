# Releasing

1. Make sure `main` is green in CI (`.github/workflows/ci.yml`: gofmt, `go vet`, `go test -race`,
   amd64 + arm64 builds).
   Before tagging, set `version` in `flake.nix` to the new version and commit it. Whenever
   `go.mod`/`go.sum` changed, also update `vendorHash` there: set it to `pkgs.lib.fakeHash`, run
   `nix build`, and copy the hash it reports (the CI `nix` job fails until it matches).
2. Tag and push:
   ```sh
   git tag -a vX.Y.Z -m "tidefiles vX.Y.Z"
   git push origin main vX.Y.Z
   ```
   The tag starts `.github/workflows/release.yml`, which runs GoReleaser (`.goreleaser.yaml`): it
   creates the GitHub release "vX.Y.Z" — always a normal release, never a pre-release, so it shows
   as Latest and `/releases/latest/download/…` links work — and attaches, for linux amd64 and
   arm64: `tidefiles_linux_<arch>.tar.gz`, `tidefiles_<arch>.deb` / `.rpm` / `.apk`, and
   `checksums.txt`. tidefiles is Linux only.
3. Add the release notes once the workflow has finished:
   ```sh
   gh release edit vX.Y.Z --notes-file notes.md
   ```
   (GoReleaser keeps existing notes, so re-running the workflow never overwrites them.)
4. Update the Arch package to the new version:
   ```sh
   cd contrib/arch
   sed -i 's/^pkgver=.*/pkgver=X.Y.Z/; s/^pkgrel=.*/pkgrel=1/' PKGBUILD
   updpkgsums && makepkg --printsrcinfo > .SRCINFO
   makepkg -f   # builds, runs the tests, packages
   ```
   and commit it.

Try a release build locally without publishing: `goreleaser release --snapshot --clean` (output in
`dist/`, which is git-ignored).

## AUR (when registration reopens)

GoReleaser already generates `tidefiles-bin` (built from the release tarballs) into `dist/aur/`.
To publish it on every release: create an AUR account, add an SSH public key under My Account, put
the private key in the repository secret `AUR_KEY`, pass it to GoReleaser in `release.yml`
(`AUR_KEY: ${{ secrets.AUR_KEY }}`), and set `skip_upload: false` under `aurs:` in
`.goreleaser.yaml`. The source package in `contrib/arch` can be submitted as `tidefiles` the same way.
