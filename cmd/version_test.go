package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bezilla/capsize/internal/buildinfo"
)

// restore puts the build-metadata globals back after a test mutates them.
func restore(t *testing.T) {
	t.Helper()
	v, c, d := buildinfo.Version, buildinfo.Commit, buildinfo.Date
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit, buildinfo.Date = v, c, d })
}

func runVersion(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	t.Cleanup(func() { versionCmd.SetOut(nil) })
	versionCmd.Run(versionCmd, nil)
	return buf.String()
}

func TestVersionReportsInjectedLdflags(t *testing.T) {
	restore(t)
	buildinfo.Version, buildinfo.Commit, buildinfo.Date = "v0.1.0", "abc1234", "2026-08-27T12:00:00Z"

	out := runVersion(t)
	for _, want := range []string{"capsize v0.1.0", "abc1234", "2026-08-27T12:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "go:") {
		t.Errorf("version output should record the toolchain:\n%s", out)
	}
}

func TestVersionOmitsEmptyBuildMetadata(t *testing.T) {
	restore(t)
	buildinfo.Version, buildinfo.Commit, buildinfo.Date = "v0.1.0", "", ""

	out := runVersion(t)
	if strings.Contains(out, "commit:") || strings.Contains(out, "built:") {
		t.Errorf("a locally built binary should not print blank metadata fields:\n%s", out)
	}
	if !strings.Contains(out, "capsize v0.1.0") {
		t.Errorf("version line missing:\n%s", out)
	}
}

func TestResolveFallsBackToDev(t *testing.T) {
	restore(t)
	buildinfo.Version = "dev"
	// Under `go test` there is no module version to read, so this exercises
	// the final fallback.
	if got := buildinfo.Resolve(); got == "" {
		t.Fatal("buildinfo.Resolve must never return empty")
	}
}
