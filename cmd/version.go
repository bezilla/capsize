package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is overridden at release time with -ldflags "-X ...cmd.version=v1.2.3".
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the capsize version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		v := version
		if v == "dev" {
			if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				v = bi.Main.Version
			}
		}
		fmt.Fprintln(cmd.OutOrStdout(), "capsize", v)
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
