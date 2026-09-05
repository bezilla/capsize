package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/bezilla/capsize/internal/detect"
	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// fixture builds the canonical demonstration cluster: one unbounded workload
// crowded onto a spot node, one well-shaped workload, one ungoverned
// namespace, and metrics present so the contradiction can be found.
func fixture() (*model.Inventory, map[model.Ref]risk.Score, []detect.Finding) {
	api := model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: "api"}
	tidy := model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: "tidy"}

	node := &model.Node{
		Name: "ip-10-0-1-5", AllocatableMem: 16 * units.Gi, AllocatableCPU: 8000,
		Spot: true, SpotEvidence: "karpenter.sh/capacity-type=spot",
		Tenants: map[model.Ref]bool{api: true, tidy: true},
	}
	for _, n := range []string{"c", "d", "e", "f", "g"} {
		node.Tenants[model.Ref{Kind: model.KindDaemonSet, Namespace: "kube-system", Name: n}] = true
	}

	inv := &model.Inventory{
		Context: "prod-eks-1", Scope: "all namespaces",
		Nodes: []*model.Node{node},
		Workloads: []*model.Workload{
			{Ref: api, Replicas: 4, RunningPods: 4, Nodes: []string{"ip-10-0-1-5"},
				Containers: []model.Container{{Name: "app", HasMemRequest: true, MemRequest: units.Gi}}},
			{Ref: tidy, Replicas: 1, RunningPods: 1, Nodes: []string{"ip-10-0-1-5"},
				Containers: []model.Container{{
					Name: "app", HasMemRequest: true, MemRequest: 256 * units.Mi,
					HasMemLimit: true, MemLimit: 320 * units.Mi,
					HasCPURequest: true, CPURequest: 100, HasCPULimit: true, CPULimit: 500,
				}}},
		},
		Namespaces: []*model.Namespace{
			{Name: "prod", Workloads: 2, LimitRanges: []string{"defaults"}},
			{Name: "sandbox", Workloads: 0},
		},
		Usage:            map[model.Ref]model.Usage{api: {MemBytes: 200 * units.Mi, Samples: 4}},
		MetricsAvailable: true,
	}

	scores := risk.All(inv, risk.Options{RequestFloor: 10 * units.Mi})
	findings := detect.Run(inv, scores, detect.Options{Divergence: 2, Headroom: 1.25})
	return inv, scores, findings
}

func render(t *testing.T, top int) string {
	t.Helper()
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)
	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true, Top: top}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestTableLeadsWithTheContradiction(t *testing.T) {
	out := render(t, 0)
	t.Log("\n" + out)

	banner := strings.Index(out, "would increase blast radius")
	table := strings.Index(out, "BLAST RADIUS")
	if banner < 0 {
		t.Fatal("the contradiction banner is the headline and must be present")
	}
	if banner > table {
		t.Error("the contradiction banner must appear above the table, not below it")
	}
	for _, want := range []string{
		"CONTRADICTIONS (1)",
		"prod/api",
		"set a memory limit",
		"ip-10-0-1-5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q", want)
		}
	}
}

func TestTableSortsByRiskDescending(t *testing.T) {
	out := render(t, 0)
	api := strings.Index(out, "prod/api")
	tidy := strings.Index(out, "prod/tidy")
	if api < 0 || tidy < 0 {
		t.Fatalf("both workloads should be listed:\n%s", out)
	}
	if api > tidy {
		t.Error("the unbounded workload must sort above the well-shaped one")
	}
}

func TestUnboundedCeilingIsMarkedAndExplained(t *testing.T) {
	out := render(t, 0)
	if !strings.Contains(out, "16Gi*") {
		t.Error("a node-derived ceiling must be marked with * so the number is not mistaken for a limit")
	}
	if !strings.Contains(out, "* ceiling is the node's allocatable memory") {
		t.Error("the * must be explained in the same output")
	}
}

func TestTopHidesRowsButSaysSo(t *testing.T) {
	out := render(t, 1)
	if strings.Contains(out, "prod/tidy") {
		t.Error("--top 1 should have hidden the second workload")
	}
	if !strings.Contains(out, "hidden by --top") {
		t.Error("truncation must be stated; a silently short table reads as a complete one")
	}
}

func TestMissingMetricsIsStatedNotOmitted(t *testing.T) {
	inv, _, _ := fixture()
	inv.MetricsAvailable = false
	inv.MetricsNote = "metrics-server not installed"
	scores := risk.All(inv, risk.Options{RequestFloor: 10 * units.Mi})
	r := Build(inv, scores, detect.Run(inv, scores, detect.Options{}), nil)

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "metrics-server not installed") {
		t.Error("capsize must say why usage findings are absent")
	}
	if !strings.Contains(out, "static findings are unaffected") {
		t.Error("degraded output must say what is still trustworthy")
	}
	if strings.Contains(out, "CONTRADICTIONS") {
		t.Error("without usage data there is no rightsizing recommendation to contradict")
	}
}

func TestWarningsAreSurfacedInTheReport(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, []string{"cannot list nodes (forbidden)"})
	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "cannot list nodes") {
		t.Error("collection warnings change what the numbers mean and belong in the report")
	}
}

func TestNoColorProducesNoEscapeSequences(t *testing.T) {
	if out := render(t, 0); strings.Contains(out, "\x1b[") {
		t.Error("--no-color must emit no ANSI escapes")
	}
}

func TestJSONCarriesTheSameFacts(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)

	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}

	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("capsize --json must emit valid JSON: %v", err)
	}
	if back.Context != "prod-eks-1" || len(back.Rows) != len(r.Rows) {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if len(back.Contradictions()) != 1 {
		t.Error("the contradiction must survive into JSON; CI reads this, not the table")
	}
	if back.MaxRisk != r.MaxRisk {
		t.Errorf("maxRisk = %v, want %v", back.MaxRisk, r.MaxRisk)
	}
	// A NaN or Inf would make encoding/json fail outright; the request floor
	// exists to prevent exactly that.
	if strings.Contains(buf.String(), "NaN") || strings.Contains(buf.String(), "Inf") {
		t.Error("scores must stay finite so the JSON stays machine-readable")
	}
}

func TestHiddenSystemCountIsAlwaysStated(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)
	r.HiddenSystemWorkloads = 9
	r.HiddenSystemNamespaces = 5

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"9 system workload(s)",
		"5 system namespace(s)",
		"hidden - use --include-system to show them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// It has to be near the top, not buried under the tables.
	if strings.Index(out, "hidden - use --include-system") > strings.Index(out, "BLAST RADIUS") {
		t.Error("the hidden-count line must appear above the table, where it will be read")
	}
}

func TestNothingHiddenMeansNoHiddenLine(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "--include-system") {
		t.Error("a complete scan must not claim anything was hidden")
	}
}

func TestWorkloadsOnlyHiddenCountStillReported(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)
	r.HiddenSystemWorkloads = 2

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "2 system workload(s) hidden") {
		t.Errorf("expected a workload-only hidden line:\n%s", out)
	}
	if strings.Contains(out, "namespace(s)") {
		t.Error("must not report zero hidden namespaces as if some were hidden")
	}
}

func TestHiddenCountsSurviveIntoJSON(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)
	r.HiddenSystemWorkloads = 9
	r.HiddenSystemNamespaces = 5

	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.HiddenSystemWorkloads != 9 || back.HiddenSystemNamespaces != 5 {
		t.Errorf("hidden counts lost in JSON: %d/%d", back.HiddenSystemWorkloads, back.HiddenSystemNamespaces)
	}
}

// The JSON schema is a contract. "neighbors" was renamed from the British
// spelling in v0.1.1, before anyone was consuming it; this pins the American
// spelling so the field cannot drift back.
func TestJSONUsesAmericanSpelling(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)

	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()

	if !strings.Contains(raw, `"neighbors"`) {
		t.Error(`the JSON report must expose the score field as "neighbors"`)
	}
	// The British spelling is the thing under test, not a typo.
	if strings.Contains(raw, "neighbour") { //nolint:misspell // asserting the old spelling is gone
		t.Error(`the old spelling must not appear anywhere in the JSON output`)
	}

	// And the value has to survive the rename, not just the key.
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range back.Rows {
		if row.Score.Neighbors > 0 {
			found = true
		}
	}
	if !found {
		t.Error("no row round-tripped a non-zero neighbor count; the rename lost the value")
	}
}

// countRenderedSeverities parses the rendered report back into a severity
// tally, reading only what a human can actually see on the screen.
func countRenderedSeverities(out string) Counts {
	var c Counts
	// Every finding line, in both the CONTRADICTIONS and FINDINGS sections,
	// is "  <severity> <rule>  <subject>  <title>".
	re := regexp.MustCompile(`(?m)^  (critical|warn|info) +(CAP\d+) `)
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "critical":
			c.Critical++
		case "warn":
			c.Warn++
		default:
			c.Info++
		}
	}
	return c
}

// TestRenderedSeveritiesMatchTheSummary is the regression guard for a printed
// contradiction: the summary counted CAP301 in its severity tally while the
// FINDINGS list deliberately excluded it, so the output claimed more criticals
// than it showed and a larger total than it listed. On a tool that asks to be
// trusted on scoring, arithmetic the reader cannot reproduce is a bug.
func TestRenderedSeveritiesMatchTheSummary(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)
	want := r.Counts

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// The fixture has to actually contain a contradiction, or this test would
	// pass vacuously on the one arrangement it exists to check.
	if len(r.Contradictions()) == 0 {
		t.Fatal("fixture must produce a contradiction for this test to mean anything")
	}

	if got := countRenderedSeverities(out); got != want {
		t.Errorf("the summary says %d critical / %d warn / %d info, "+
			"but the output shows %d / %d / %d:\n%s",
			want.Critical, want.Warn, want.Info,
			got.Critical, got.Warn, got.Info, out)
	}
}

// TestFindingsHeadingReconcilesWithTheTotal checks the other half: the
// FINDINGS heading lists fewer findings than the summary totals, and it has to
// say so rather than leaving the reader to spot the gap.
func TestFindingsHeadingReconcilesWithTheTotal(t *testing.T) {
	inv, scores, findings := fixture()
	r := Build(inv, scores, findings, nil)

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	listed := r.Counts.Total() - len(r.Contradictions())
	want := fmt.Sprintf("FINDINGS (%d OF %d; %d LISTED ABOVE UNDER CONTRADICTIONS)",
		listed, r.Counts.Total(), len(r.Contradictions()))
	if !strings.Contains(out, want) {
		t.Errorf("expected heading %q:\n%s", want, out)
	}
	if strings.Contains(out, fmt.Sprintf("FINDINGS (%d)\n", listed)) {
		t.Error("a bare count in the heading contradicts the summary total")
	}
}

// TestFindingsHeadingIsPlainWithoutContradictions keeps the reconciling text
// from becoming noise on the ordinary path, where the two counts agree.
func TestFindingsHeadingIsPlainWithoutContradictions(t *testing.T) {
	inv, _, _ := fixture()
	inv.MetricsAvailable = false
	inv.Usage = nil
	scores := risk.All(inv, risk.Options{RequestFloor: 10 * units.Mi})
	r := Build(inv, scores, detect.Run(inv, scores, detect.Options{}), nil)

	var buf bytes.Buffer
	if err := Table(&buf, r, TableOptions{NoColor: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if len(r.Contradictions()) != 0 {
		t.Fatal("this fixture is supposed to have no contradiction")
	}
	if !strings.Contains(out, fmt.Sprintf("FINDINGS (%d)", r.Counts.Total())) {
		t.Errorf("with nothing hidden the heading should be a bare count:\n%s", out)
	}
	if got := countRenderedSeverities(out); got != r.Counts {
		t.Errorf("severity tally still has to match: summary %+v, rendered %+v\n%s", r.Counts, got, out)
	}
}
