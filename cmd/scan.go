package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/bezilla/capsize/internal/collect"
	"github.com/bezilla/capsize/internal/detect"
	"github.com/bezilla/capsize/internal/k8s"
	"github.com/bezilla/capsize/internal/output"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// runScan is the whole pipeline: connect, collect, score, detect, render, gate.
func runScan(ctx context.Context, o *Options, out io.Writer) error {
	tuning, failOn, err := validate(o)
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

	scores := risk.All(inv, risk.Options{RequestFloor: tuning.requestFloor})
	findings := detect.Run(inv, scores, detect.Options{
		Divergence: o.Divergence,
		Headroom:   o.Headroom,
		MinRequest: tuning.minRequest,
		IdleRatio:  o.IdleRatio,
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

// tuning holds the parsed quantity flags.
type tuning struct {
	requestFloor int64 // scoring: assumed request for a workload declaring none
	minRequest   int64 // advice: smallest request worth recommending
}

// validate turns the flag strings into values, failing before any cluster
// call so a typo costs nothing.
func validate(o *Options) (t tuning, failOn detect.Severity, err error) {
	if t.requestFloor, err = quantity("--request-floor", o.RequestFloorStr); err != nil {
		return tuning{}, "", err
	}
	if t.minRequest, err = quantity("--min-request", o.MinRequestStr); err != nil {
		return tuning{}, "", err
	}

	if o.Divergence <= 1 {
		return tuning{}, "", fmt.Errorf("--divergence must be greater than 1, got %v", o.Divergence)
	}
	if o.Headroom < 1 {
		return tuning{}, "", fmt.Errorf("--headroom must be at least 1, got %v", o.Headroom)
	}
	if o.IdleRatio <= o.Divergence {
		return tuning{}, "", fmt.Errorf(
			"--idle-ratio must be greater than --divergence (%v), got %v; below it there would be "+
				"no band in which capsize recommends anything", o.Divergence, o.IdleRatio)
	}

	if strings.EqualFold(o.FailOn, "none") || o.FailOn == "" {
		return t, "", nil
	}
	sev, ok := detect.ParseSeverity(strings.ToLower(o.FailOn))
	if !ok {
		return tuning{}, "", fmt.Errorf("--fail-on %q: want one of none, info, warn, critical", o.FailOn)
	}
	return t, sev, nil
}

func quantity(flag, raw string) (int64, error) {
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %w", flag, raw, err)
	}
	if q.Value() <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero, got %q", flag, raw)
	}
	return q.Value(), nil
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
