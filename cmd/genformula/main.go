// Command genformula renders the Homebrew formula for a tagged cupthread
// release. The release workflow (`.github/workflows/release.yml`) runs it on
// every `v*` tag to write the `CupThread/homebrew-tap` formula and this
// repo's `Formula/` mirror; there is no hand-maintained formula file.
//
//	go run ./cmd/genformula -version 0.3.0 -sha256 <64-hex-digest>
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/CupThread/CupThreadAgenticCoding/internal/release"
)

func main() {
	version := flag.String("version", "", "release version without the leading v (e.g. 0.3.0)")
	sha256 := flag.String("sha256", "", "sha256 of the tag's source tarball on GitHub (64 hex chars)")
	repo := flag.String("repo", "", "GitHub owner/name (default CupThread/CupThreadAgenticCoding)")
	flag.Parse()

	out, err := (release.Formula{Repo: *repo, Version: *version, SHA256: *sha256}).Render()
	if err != nil {
		fmt.Fprintln(os.Stderr, "genformula:", err)
		flag.Usage()
		os.Exit(1)
	}
	os.Stdout.WriteString(out)
}
