package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Exit codes. 1 is reserved for operational failure (couldn't reach the API,
// bad flags); 2 means the scan succeeded and something crossed a threshold.
// CI can therefore tell "capsize broke" apart from "capsize found something".
const (
	exitOK        = 0
	exitError     = 1
	exitThreshold = 2
)

// Options is the full flag surface. It is a plain struct so the scan pipeline
// can be driven from a test without going through cobra.
type Options struct {
	// Connection
	Kubeconfig string
	Context    string
	Namespace  string
	AllNS      bool

	// Analysis tuning
	RequestFloorStr string  // assumed memory request for a workload declaring none
	Divergence      float64 // request/usage factor above which a request is "far divergent"
	Headroom        float64 // safety multiplier applied to observed usage when recommending
	NoMetrics       bool    // skip metrics-server entirely

	// Output
	JSON    bool
	Top     int
	NoColor bool

	// CI gating
	RiskThreshold float64
	FailOn        string
}

var opts Options

var rootCmd = &cobra.Command{
	Use:   "capsize",
	Short: "Score Kubernetes cost waste and blast-radius exposure together",
	Long: `capsize is a read-only Kubernetes CLI that scores two things at once:

  cost waste     - missing or badly-shaped resource requests and limits
  blast radius   - how much of a node a single workload can consume before
                   the kernel starts killing its neighbours

It reports where those two scores disagree: the workloads where the obvious
cost recommendation ("your request is 4x your usage, shrink it") would make an
outage worse. That contradiction is the reason this tool exists.

capsize never writes to your cluster. It uses your existing kubeconfig and
issues GET and LIST only; the HTTP transport rejects every other verb.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScan(cmd.Context(), &opts, cmd.OutOrStdout())
	},
}

// Execute runs the root command and maps errors onto capsize's exit codes.
func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		os.Exit(exitOK)
	}
	if _, ok := err.(thresholdError); ok {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitThreshold)
	}
	fmt.Fprintln(os.Stderr, "capsize:", err)
	os.Exit(exitError)
}

// thresholdError signals "the scan worked, the cluster is the problem".
type thresholdError struct{ msg string }

func (e thresholdError) Error() string { return e.msg }

func init() {
	f := rootCmd.PersistentFlags()

	f.StringVar(&opts.Kubeconfig, "kubeconfig", "", "path to kubeconfig (default: $KUBECONFIG, then ~/.kube/config)")
	f.StringVar(&opts.Context, "context", "", "kubeconfig context to use (default: current-context)")
	f.StringVarP(&opts.Namespace, "namespace", "n", "", "namespace to scan (default: the context's namespace)")
	f.BoolVarP(&opts.AllNS, "all-namespaces", "A", false, "scan every namespace the caller can read")

	f.StringVar(&opts.RequestFloorStr, "request-floor", "10Mi", "memory request assumed for a workload that declares none, so its ratio stays finite")
	f.Float64Var(&opts.Divergence, "divergence", 2.0, "flag a request when it exceeds observed usage by this factor")
	f.Float64Var(&opts.Headroom, "headroom", 1.25, "safety multiplier applied to observed usage when recommending a request")
	f.BoolVar(&opts.NoMetrics, "no-metrics", false, "do not query metrics-server, even if it is reachable")

	f.BoolVar(&opts.JSON, "json", false, "emit the full report as JSON instead of a table")
	f.IntVar(&opts.Top, "top", 0, "show only the N riskiest workloads (0 = all)")
	f.BoolVar(&opts.NoColor, "no-color", false, "disable colour in the table output")

	f.Float64Var(&opts.RiskThreshold, "risk-threshold", 0, "exit 2 if any workload's risk score exceeds this (0 = never)")
	f.StringVar(&opts.FailOn, "fail-on", "none", "exit 2 if any finding is at or above this severity: none|info|warn|critical")
}
