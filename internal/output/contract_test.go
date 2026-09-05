package output

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bezilla/capsize/internal/detect"
	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// --update rewrites the schema file for the CURRENT SchemaVersion. It is the
// deliberate second half of a version bump, not a way to make a red test go
// green: if the shape changed, bump SchemaVersion first, and the file this
// writes will be a new one that shows up in review as an addition.
var update = flag.Bool("update", false, "rewrite the schema file for the current SchemaVersion")

// contractReport builds a report that reaches every field in the document,
// including the ones behind `omitempty`. A field that no fixture populates is
// a field this test cannot defend, so the fixture is deliberately maximal
// rather than realistic.
func contractReport() *Report {
	api := model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: "api"}
	idle := model.Ref{Kind: model.KindCronJob, Namespace: "prod", Name: "nightly"}
	lonely := model.Ref{Kind: model.KindStatefulSet, Namespace: "prod", Name: "lonely"}

	spot := &model.Node{
		Name: "ip-10-0-1-5", AllocatableMem: 16 * units.Gi, AllocatableCPU: 8000,
		Spot: true, SpotEvidence: "karpenter.sh/capacity-type=spot",
		Tenants: map[model.Ref]bool{api: true, lonely: true},
	}

	inv := &model.Inventory{
		Context: "prod-eks-1", Scope: "all namespaces",
		Nodes: []*model.Node{spot},
		Workloads: []*model.Workload{
			// Scored, on a real node, with a sidecar and defaulted requests so
			// every Container flag is exercised.
			{Ref: api, Replicas: 4, RunningPods: 4, Nodes: []string{"ip-10-0-1-5"},
				Containers: []model.Container{
					{Name: "app", HasMemRequest: true, MemRequest: units.Gi},
					{Name: "proxy", Sidecar: true,
						HasMemRequest: true, MemRequest: 64 * units.Mi, MemRequestDefaulted: true,
						HasMemLimit: true, MemLimit: 64 * units.Mi,
						HasCPURequest: true, CPURequest: 100, CPURequestDefaulted: true,
						HasCPULimit: true, CPULimit: 100},
				}},
			// No running pods: forces score.hypothetical.
			{Ref: idle, Replicas: 1, RunningPods: 0,
				Containers: []model.Container{{Name: "app", HasMemRequest: true, MemRequest: 128 * units.Mi}}},
			// Sole tenant, so the zero-risk-but-not-safe branch is present too.
			{Ref: lonely, Replicas: 1, RunningPods: 1, Nodes: []string{"ip-10-0-1-5"},
				Containers: []model.Container{{Name: "app", HasMemRequest: true, MemRequest: 256 * units.Mi}}},
		},
		Namespaces: []*model.Namespace{
			{Name: "prod", Workloads: 3, LimitRanges: []string{"defaults"}, ResourceQuotas: []string{"cap"}},
			{Name: "sandbox", Workloads: 0},
		},
		Usage:            map[model.Ref]model.Usage{api: {MemBytes: 200 * units.Mi, CPUMillis: 40, Samples: 4}},
		MetricsAvailable: true,
		MetricsNote:      "metrics-server is registered",
	}

	scores := risk.All(inv, risk.Options{RequestFloor: 10 * units.Mi})
	findings := detect.Run(inv, scores, detect.Options{Divergence: 2, Headroom: 1.25, MinRequest: 32 * units.Mi, IdleRatio: 50})

	// A workload with no node at all: forces score.scored=false and
	// score.reason onto a row.
	unscored := model.Ref{Kind: model.KindDaemonSet, Namespace: "prod", Name: "orphaned"}
	scores[unscored] = risk.Score{Ref: unscored, Reason: "node list unavailable"}
	inv.Workloads = append(inv.Workloads, &model.Workload{Ref: unscored, Replicas: 1})

	r := Build(inv, scores, findings, []string{"cannot list nodes (forbidden)"})
	r.HiddenSystemWorkloads = 9
	r.HiddenSystemNamespaces = 5
	return r
}

// shape reduces a JSON document to the set of paths it contains and the type
// at each one. Values are deliberately discarded: a score that moves with node
// size is not a contract change, and a field that appears, vanishes or changes
// type is.
func shape(v any, path string, into map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		if path != "" {
			into[path] = "object"
		}
		for k, child := range t {
			next := k
			if path != "" {
				next = path + "." + k
			}
			shape(child, next, into)
		}
	case []any:
		into[path] = "array"
		// Merge every element, so a nil Recommendation on one finding does not
		// hide the field that another finding carries.
		for _, child := range t {
			shape(child, path+"[]", into)
		}
	case string:
		into[path] = "string"
	case float64:
		into[path] = "number"
	case bool:
		into[path] = "boolean"
	case nil:
		into[path] = "null"
	default:
		into[path] = fmt.Sprintf("unknown(%T)", v)
	}
}

func schemaPath(version string) string {
	return filepath.Join("testdata", "schema-"+version+".json")
}

// TestJSONContract is the gate on the --json shape. It fails when a field is
// added, removed, renamed or retyped without SchemaVersion being bumped,
// because the schema file it compares against is named after the version.
func TestJSONContract(t *testing.T) {
	// Freeze the clock so the document is a function of the fixture alone.
	realNow := now
	now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = realNow })

	r := contractReport()
	r.ToolVersion = "v0.0.0-contract"

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("the report must always marshal: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("capsize --json must emit valid JSON: %v", err)
	}

	got := map[string]string{}
	shape(doc, "", got)

	path := schemaPath(SchemaVersion)
	if *update {
		writeSchema(t, path, got)
		t.Logf("wrote %s for schemaVersion %s", path, SchemaVersion)
		return
	}

	wantRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no schema file for schemaVersion %s.\n"+
			"If you bumped SchemaVersion on purpose, record the new shape:\n"+
			"    go test ./internal/output -run TestJSONContract -update\n"+
			"and commit %s alongside the bump.\n%v", SchemaVersion, path, err)
	}
	var want map[string]string
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("%s is not readable: %v", path, err)
	}

	var added, removed, retyped []string
	for k, v := range got {
		switch w, ok := want[k]; {
		case !ok:
			added = append(added, fmt.Sprintf("+ %s (%s)", k, v))
		case w != v:
			retyped = append(retyped, fmt.Sprintf("~ %s: %s -> %s", k, w, v))
		}
	}
	for k, w := range want {
		if _, ok := got[k]; !ok {
			removed = append(removed, fmt.Sprintf("- %s (%s)", k, w))
		}
	}
	diff := append(append(added, removed...), retyped...)
	if len(diff) == 0 {
		return
	}
	sort.Strings(diff)
	t.Errorf(`the --json shape changed but schemaVersion is still %s:

%s

--json is advertised for automation, so a consumer somewhere is indexing
these paths. Either revert the change, or bump SchemaVersion in
internal/output/report.go, record the new shape with

    go test ./internal/output -run TestJSONContract -update

and describe the change in docs/json-contract.md and CHANGELOG.md.`,
		SchemaVersion, strings.Join(diff, "\n"))
}

// TestJSONContractCoversEveryEnvelopeField keeps the envelope itself honest:
// the three fields that exist so a consumer can version-check are the three
// most likely to be dropped by a refactor that "only touches formatting".
func TestJSONContractCoversEveryEnvelopeField(t *testing.T) {
	r := contractReport()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["schemaVersion"] != SchemaVersion {
		t.Errorf("schemaVersion = %v, want %q", back["schemaVersion"], SchemaVersion)
	}
	if s, _ := back["toolVersion"].(string); s == "" {
		t.Error("toolVersion must never be empty; a blank version is worse than an honest \"dev\"")
	}
	if s, _ := back["generatedAt"].(string); s == "" {
		t.Error("generatedAt must be present so a stored report can be aged")
	} else if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("generatedAt must be RFC 3339, got %q: %v", s, err)
	}
}

func writeSchema(t *testing.T, path string, got map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
