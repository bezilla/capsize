// Package buildinfo holds the build stamps injected at release time.
//
// They live here rather than in cmd because they are a fact about the module,
// not about the command line: the JSON report carries the tool version so a
// machine consuming it can tell which capsize produced it, and internal/output
// cannot import cmd.
//
// The variables are exported and mutable because that is the only shape the
// linker can write to. Nothing should assign to them outside a test; read them
// through the accessors below.
package buildinfo

import "runtime/debug"

// Build metadata, injected by goreleaser via
// -ldflags "-X github.com/bezilla/capsize/internal/buildinfo.Version=v1.2.3 ...".
var (
	// Version is the release tag, or "dev" for a local build.
	Version = "dev"
	// Commit is the full git SHA of the release, empty for a local build.
	Commit = ""
	// Date is the RFC 3339 build timestamp, empty for a local build.
	Date = ""
)

// Resolve prefers the injected version, then the module version Go records for
// a `go install`ed binary, and falls back to "dev" for a local `go build`
// where neither is available. It never returns an empty string, because a
// blank version in a machine-readable report is worse than an honest "dev".
func Resolve() string {
	if Version != "dev" && Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
