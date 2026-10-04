# Releasing

1. Make sure `main` is green in CI (`.github/workflows/ci.yml`: gofmt, `go vet`, `go test -race`,
   amd64 + arm64 builds).
2. Tag and push:
   ```sh
   git tag -a vX.Y.Z -m "tidefiles vX.Y.Z"
   git push origin main vX.Y.Z
   ```
   The tag starts `.github/workflows/release.yml`, which runs GoReleaser (`.goreleaser.yaml`): it
   creates the GitHub release "vX.Y.Z (preview)" (a pre-release while we're below 1.0) and attaches
   static linux amd64/arm64 tarballs and `checksums.txt`.
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
