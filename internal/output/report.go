// Package output assembles a scan into a Report and renders it, either as a
// table for a human or as JSON for a machine. Both renderers read the same
// Report, so the two can never drift.
package output

import (
	"sort"
	"time"

	"github.com/bezilla/capsize/internal/buildinfo"
	"github.com/bezilla/capsize/internal/detect"
	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
)

// SchemaVersion is the version of the --json document shape, independent of
// the capsize release that produced it. See docs/json-contract.md for what a
// consumer may rely on and what each component of the number means.
//
// A change to this constant without a corresponding change to
// internal/output/testdata/schema-<version>.json fails TestJSONContract, and
// vice versa. That is the whole point of it.
const SchemaVersion = "1.0.0"

// now is the clock Build stamps GeneratedAt from. A test replaces it; nothing
// else should.
var now = time.Now

// Row is one workload as it appears in the blast-radius table.
type Row struct {
	Ref         model.Ref  `json:"ref"`
	Replicas    int32      `json:"replicas"`
	RunningPods int        `json:"runningPods"`
	Score       risk.Score `json:"score"`

	// Findings counts the findings attributed to this workload, and
	// Contradictions how many of them are CAP301.
	Findings       int `json:"findings"`
	Contradictions int `json:"contradictions"`
}

// Counts summarizes a findings list.
type Counts struct {
	Critical int `json:"critical"`
	Warn     int `json:"warn"`
	Info     int `json:"info"`
}

// Total is the number of findings across all severities.
func (c Counts) Total() int { return c.Critical + c.Warn + c.Info }

// Report is the complete result of one scan.
//
// The first three fields are the envelope: they say what this document is,
// what produced it and when, so a consumer that stored a report last quarter
// can tell whether it is still reading the shape it was written against.
type Report struct {
	SchemaVersion string    `json:"schemaVersion"`
	ToolVersion   string    `json:"toolVersion"`
	GeneratedAt   time.Time `json:"generatedAt"`

	Context string `json:"context"`
	Scope   string `json:"scope"`

	MetricsAvailable bool   `json:"metricsAvailable"`
	MetricsNote      string `json:"metricsNote,omitempty"`

	// Warnings are the things capsize could not read. They are part of the
	// report rather than stderr noise, because a risk score computed without
	// the node list means something different from one computed with it.
	Warnings []string `json:"warnings,omitempty"`

	Rows       []Row              `json:"workloads"`
	Findings   []detect.Finding   `json:"findings"`
	Namespaces []*model.Namespace `json:"namespaces"`

	Counts Counts `json:"counts"`

	// HiddenSystemWorkloads and HiddenSystemNamespaces record what the default
	// scope left out. They are always rendered when non-zero: a count the
	// reader cannot see is indistinguishable from a clean scan.
	HiddenSystemWorkloads  int `json:"hiddenSystemWorkloads,omitempty"`
	HiddenSystemNamespaces int `json:"hiddenSystemNamespaces,omitempty"`

	// MaxRisk is the highest score in the report, which is what
	// --risk-threshold gates on.
	MaxRisk    float64    `json:"maxRisk"`
	MaxRiskRef *model.Ref `json:"maxRiskRef,omitempty"`

	// Scored is how many workloads got a blast-radius score; Unscored is how
	// many could not be scored (no pods and no nodes to forecast against).
	Scored   int `json:"scored"`
	Unscored int `json:"unscored"`
}

// Build assembles a Report. Rows are sorted by risk, descending, which is the
// order the table prints and the order the JSON carries.
func Build(inv *model.Inventory, scores map[model.Ref]risk.Score, findings []detect.Finding, warnings []string) *Report {
	r := &Report{
		SchemaVersion:    SchemaVersion,
		ToolVersion:      buildinfo.Resolve(),
		GeneratedAt:      now().UTC(),
		Context:          inv.Context,
		Scope:            inv.Scope,
		MetricsAvailable: inv.MetricsAvailable,
		MetricsNote:      inv.MetricsNote,
		Warnings:         warnings,
		Findings:         findings,
	}

	perWorkload := map[model.Ref]int{}
	contradictions := map[model.Ref]int{}
	for _, f := range findings {
		switch f.Severity {
		case detect.SeverityCritical:
			r.Counts.Critical++
		case detect.SeverityWarn:
			r.Counts.Warn++
		default:
			r.Counts.Info++
		}
		if f.Ref == nil {
			continue
		}
		perWorkload[*f.Ref]++
		if f.Rule == detect.RuleContradiction {
			contradictions[*f.Ref]++
		}
	}

	for _, w := range inv.Workloads {
		s := scores[w.Ref]
		if s.Scored {
			r.Scored++
		} else {
			r.Unscored++
		}
		r.Rows = append(r.Rows, Row{
			Ref: w.Ref, Replicas: w.Replicas, RunningPods: w.RunningPods,
			Score: s, Findings: perWorkload[w.Ref], Contradictions: contradictions[w.Ref],
		})
		if s.Scored && s.Risk > r.MaxRisk {
			ref := w.Ref
			r.MaxRisk, r.MaxRiskRef = s.Risk, &ref
		}
	}

	sort.SliceStable(r.Rows, func(i, j int) bool {
		a, b := r.Rows[i], r.Rows[j]
		if a.Score.Risk != b.Score.Risk {
			return a.Score.Risk > b.Score.Risk
		}
		if a.Ref.Namespace != b.Ref.Namespace {
			return a.Ref.Namespace < b.Ref.Namespace
		}
		return a.Ref.Name < b.Ref.Name
	})

	for _, ns := range inv.Namespaces {
		if ns.Ungoverned() {
			r.Namespaces = append(r.Namespaces, ns)
		}
	}

	return r
}

// Contradictions returns just the CAP301 findings, in report order.
func (r *Report) Contradictions() []detect.Finding {
	var out []detect.Finding
	for _, f := range r.Findings {
		if f.Rule == detect.RuleContradiction {
			out = append(out, f)
		}
	}
	return out
}

// Limit truncates the row list to the n riskiest, returning how many were
// hidden so the renderer can say so rather than silently dropping them.
func (r *Report) Limit(n int) (hidden int) {
	if n <= 0 || n >= len(r.Rows) {
		return 0
	}
	hidden = len(r.Rows) - n
	r.Rows = r.Rows[:n]
	return hidden
}
