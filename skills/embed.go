// Package skills embeds the bundled agent skills so the distributed CLI
// binary (Homebrew, go install) can list and link them without a source
// checkout.
package skills

import "embed"

// FS holds the six bundled skill directories. The all: prefix keeps hidden
// and underscore-prefixed files inside each skill.
//
//go:embed all:cupthread-android-sdk all:cupthread-api all:cupthread-cli all:cupthread-flutter-sdk all:cupthread-react-native-sdk all:cupthread-swift-sdk
var FS embed.FS
