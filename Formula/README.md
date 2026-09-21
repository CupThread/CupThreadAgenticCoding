# Homebrew formula

The formula published to `CupThread/homebrew-tap` is **generated**, not
hand-maintained. On every `vX.Y.Z` tag push the release workflow
(`.github/workflows/release.yml`):

1. verifies the test suite, builds the CLI with the tag injected as the
   version via `-ldflags -X`, and refuses to release unless the built binary
   reports exactly the tagged version;
2. renders the formula with `go run ./cmd/genformula`, pinning the tag's
   source tarball URL and sha256, and injects the same version at build time
   inside `install`;
3. pushes the formula to the first-party tap — this is what makes
   `brew upgrade cupthread` fire — and commits a copy back to this
   directory.

Between releases this directory intentionally has no formula: pinning a tag
that does not exist yet would break installs. To preview the next release's
formula:

```sh
go run ./cmd/genformula -version 0.3.0 -sha256 <64-hex-digest>
```
