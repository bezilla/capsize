package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/pjbezilla/capsize/internal/collect"
	"github.com/pjbezilla/capsize/internal/detect"
	"github.com/pjbezilla/capsize/internal/k8s"
	"github.com/pjbezilla/capsize/internal/output"
	"github.com/pjbezilla/capsize/internal/risk"
	"github.com/pjbezilla/capsize/internal/units"
)

// runScan is the whole pipeline: connect, collect, score, detect, render, gate.
func runScan(ctx context.Context, o *Options, out io.Writer) error {
	floor, failOn, err := validate(o)
	if err != nil {
		return err
	}

	client, err := k8s.Connect(o.Kubeconfig, o.Context)
	if err != nil {
		return err
	}

	ns, err := resolveNamespace(o, client)
	if err != nil {
		return err
	}

	inv, warnings, err := collect.Collect(ctx, client, collect.Options{
		Namespace:   ns,
		WithMetrics: !o.NoMetrics,
		Context:     client.ContextName,
	})
	if err != nil {
		return err
	}

	scores := risk.All(inv, risk.Options{RequestFloor: floor})
	findings := detect.Run(inv, scores, detect.Options{
		Divergence: o.Divergence,
		Headroom:   o.Headroom,
	})
	report := output.Build(inv, scores, findings, warnings)

	if o.JSON {
		if err := output.JSON(out, report); err != nil {
			return err
		}
	} else if err := output.Table(out, report, output.TableOptions{
		NoColor: o.NoColor,
		Top:     o.Top,
	}); err != nil {
		return err
	}

	return gate(o, failOn, report)
}

// validate turns the flag strings into values, failing before any cluster
// call so a typo costs nothing.
func validate(o *Options) (floor int64, failOn detect.Severity, err error) {
	q, err := resource.ParseQuantity(o.RequestFloorStr)
	if err != nil {
		return 0, "", fmt.Errorf("--request-floor %q: %w", o.RequestFloorStr, err)
	}
	floor = q.Value()
	if floor <= 0 {
		return 0, "", fmt.Errorf("--request-floor must be greater than zero, got %q", o.RequestFloorStr)
	}

	if o.Divergence <= 1 {
		return 0, "", fmt.Errorf("--divergence must be greater than 1, got %v", o.Divergence)
	}
	if o.Headroom < 1 {
		return 0, "", fmt.Errorf("--headroom must be at least 1, got %v", o.Headroom)
	}

	if strings.EqualFold(o.FailOn, "none") || o.FailOn == "" {
		return floor, "", nil
	}
	sev, ok := detect.ParseSeverity(strings.ToLower(o.FailOn))
	if !ok {
		return 0, "", fmt.Errorf("--fail-on %q: want one of none, info, warn, critical", o.FailOn)
	}
	return floor, sev, nil
}

func resolveNamespace(o *Options, c *k8s.Client) (string, error) {
	if o.AllNS && o.Namespace != "" {
		return "", fmt.Errorf("--all-namespaces and --namespace are mutually exclusive")
	}
	if o.AllNS {
		return "", nil
	}
	if o.Namespace != "" {
		return o.Namespace, nil
	}
	return c.DefaultNamespace, nil
}

// gate implements the CI contract: exit 2 when the cluster crossed a line the
// caller drew, with the reason on stderr so a failed pipeline says why.
func gate(o *Options, failOn detect.Severity, r *output.Report) error {
	var reasons []string

	if o.RiskThreshold > 0 && r.MaxRisk > o.RiskThreshold {
		where := ""
		if r.MaxRiskRef != nil {
			where = " (" + r.MaxRiskRef.Short() + ")"
		}
		reasons = append(reasons, fmt.Sprintf("blast radius %s exceeds --risk-threshold %s%s",
			units.Score(r.MaxRisk), units.Score(o.RiskThreshold), where))
	}

	if failOn != "" {
		if worst, ok := detect.Worst(r.Findings); ok && detect.AtLeast(worst, failOn) {
			reasons = append(reasons, fmt.Sprintf("found a %s finding at or above --fail-on %s", worst, failOn))
		}
	}

	if len(reasons) == 0 {
		return nil
	}
	return thresholdError{msg: "capsize: " + strings.Join(reasons, "; ")}
}
