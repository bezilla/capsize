package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/bezilla/capsize/internal/buildinfo"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the capsize version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		// Writes to stdout: a failure here cannot be reported anywhere
		// useful, and returning it would change the exit code.
		_, _ = fmt.Fprintln(out, "capsize", buildinfo.Resolve())
		if buildinfo.Commit != "" {
			_, _ = fmt.Fprintln(out, "  commit:", buildinfo.Commit)
		}
		if buildinfo.Date != "" {
			_, _ = fmt.Fprintln(out, "  built: ", buildinfo.Date)
		}
		_, _ = fmt.Fprintf(out, "  go:     %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
