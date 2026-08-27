package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Build metadata, injected at release time by goreleaser via
// -ldflags "-X github.com/bezilla/capsize/cmd.version=v1.2.3 ...".
//
// They stay unexported: -X sets a package-level string regardless of export,
// and nothing in capsize should be reading these but the command below.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the capsize version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "capsize", resolveVersion())
		if commit != "" {
			fmt.Fprintln(out, "  commit:", commit)
		}
		if date != "" {
			fmt.Fprintln(out, "  built: ", date)
		}
		fmt.Fprintf(out, "  go:     %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

// resolveVersion prefers the injected version, then the module version Go
// records for a `go install`ed binary, and falls back to "dev" for a local
// `go build` where neither is available.
func resolveVersion() string {
	if version != "dev" && version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func init() { rootCmd.AddCommand(versionCmd) }
