package detect

import (
	"strings"
	"testing"

	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

func ref(name string) model.Ref {
	return model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: name}
}

type ctrOpt func(*model.Container)

func memReq(b int64) ctrOpt {
	return func(c *model.Container) { c.HasMemRequest, c.MemRequest = true, b }
}
func memLim(b int64) ctrOpt { return func(c *model.Container) { c.HasMemLimit, c.MemLimit = true, b } }
func cpuReq(m int64) ctrOpt {
	return func(c *model.Container) { c.HasCPURequest, c.CPURequest = true, m }
}
func cpuLim(m int64) ctrOpt { return func(c *model.Container) { c.HasCPULimit, c.CPULimit = true, m } }

// container builds a container the way collect delivers one: with Kubernetes'
// own request-defaulting already applied. Building a raw model.Container here
// would let a test assert behavior that cannot occur against a real cluster.
func container(name string, opts ...ctrOpt) model.Container {
	c := model.Container{Name: name}
	for _, o := range opts {
		o(&c)
	}
	return c.Effective()
}

// scene builds a one-node, one-workload cluster with the given neighbors.
func scene(w *model.Workload, allocGi int64, neighbors int, spot bool) (*model.Inventory, map[model.Ref]risk.Score) {
	n := &model.Node{
		Name:           "node-1",
		AllocatableMem: allocGi * units.Gi,
		AllocatableCPU: 8000,
		Spot:           spot,
		Tenants:        map[model.Ref]bool{w.Ref: true},
	}
	for i := 0; i < neighbors; i++ {
		n.Tenants[model.Ref{Kind: model.KindDeployment, Namespace: "other", Name: string(rune('a' + i))}] = true
	}
	w.Nodes = []string{"node-1"}
	w.RunningPods = 1
	inv := &model.Inventory{
		Nodes:      []*model.Node{n},
		Workloads:  []*model.Workload{w},
		Namespaces: []*model.Namespace{{Name: "prod", Workloads: 1, LimitRanges: []string{"defaults"}}},
		Usage:      map[model.Ref]model.Usage{},
	}
	return inv, risk.All(inv, risk.Options{RequestFloor: 10 * units.Mi})
}

func has(fs []Finding, rule string) *Finding {
	for i := range fs {
		if fs[i].Rule == rule {
			return &fs[i]
		}
	}
	return nil
}

func TestFlagsMissingRequestsAndLimits(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Replicas: 2, Containers: []model.Container{container("app")}}
	inv, scores := scene(w, 16, 3, false)

	fs := Run(inv, scores, Options{})
	if has(fs, RuleNoRequests) == nil {
		t.Error("a container with no requests must be flagged")
	}
	f := has(fs, RuleNoLimits)
	if f == nil {
		t.Fatal("a container with no limits must be flagged")
	}
	if f.Severity != SeverityCritical {
		t.Errorf("unbounded next to %d neighbors should be critical, got %s", 3, f.Severity)
	}
	if !strings.Contains(f.Detail, "node-1") {
		t.Errorf("the detail should name the node whose memory is the ceiling: %q", f.Detail)
	}
}

// A container declaring only limits is Guaranteed, not BestEffort. capsize
// reports it as manifest hygiene and nothing worse.
func TestImplicitRequestsAreHygieneNotHazard(t *testing.T) {
	limitOnly := &model.Workload{Ref: ref("a"), Containers: []model.Container{
		container("app", memLim(512*units.Mi), cpuLim(500)),
	}}
	inv, scores := scene(limitOnly, 16, 1, false)
	fs := Run(inv, scores, Options{})

	if f := has(fs, RuleNoRequests); f != nil {
		t.Error("CAP101 must not fire: Kubernetes defaults the requests to the limits, " +
			"so the scheduler does reserve")
	}

	mem := has(fs, RuleMemLimitNoReq)
	if mem == nil {
		t.Fatal("leaving the memory request implicit is still worth noting")
	}
	if mem.Severity != SeverityInfo {
		t.Errorf("CAP103 severity = %s, want info: the outcome is correct, just unstated", mem.Severity)
	}
	if strings.Contains(mem.Detail, "BestEffort") {
		t.Errorf("CAP103 must not claim BestEffort; this pod is Guaranteed: %q", mem.Detail)
	}
	if strings.Contains(mem.Detail, "reserves nothing") {
		t.Errorf("CAP103 must not claim the scheduler reserves nothing: %q", mem.Detail)
	}
	if !strings.Contains(mem.Detail, string(model.QoSGuaranteed)) {
		t.Errorf("CAP103 should name the real QoS class: %q", mem.Detail)
	}

	cpu := has(fs, RuleCPULimitNoReq)
	if cpu == nil {
		t.Fatal("leaving the cpu request implicit is still worth noting")
	}
	if cpu.Severity != SeverityInfo {
		t.Errorf("CAP105 severity = %s, want info", cpu.Severity)
	}
	if strings.Contains(cpu.Detail, "never guaranteed") {
		t.Errorf("CAP105 must not claim the share is unguaranteed; it is: %q", cpu.Detail)
	}
}

func TestRequestWithoutLimitIsStillFlagged(t *testing.T) {
	requestOnly := &model.Workload{Ref: ref("b"), Containers: []model.Container{
		container("app", memReq(512*units.Mi), cpuReq(500), cpuLim(1000)),
	}}
	inv, scores := scene(requestOnly, 16, 1, false)
	fs := Run(inv, scores, Options{})
	if has(fs, RuleMemReqNoLimit) == nil {
		t.Error("memory request without a limit must be flagged")
	}
	// The cpu request was declared, not defaulted, so CAP105 has nothing to say.
	if has(fs, RuleCPULimitNoReq) != nil {
		t.Error("CAP105 must not fire when the request was declared explicitly")
	}
}

func TestNoGuardrailsFiresOnlyWhenBothAreAbsent(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Containers: []model.Container{
		container("app", memReq(256*units.Mi), memLim(256*units.Mi)),
	}}
	inv, scores := scene(w, 16, 1, false)

	if fs := Run(inv, scores, Options{}); has(fs, RuleNoGuardrails) != nil {
		t.Error("a namespace with a LimitRange must not be flagged")
	}

	inv.Namespaces[0].LimitRanges = nil
	if fs := Run(inv, scores, Options{}); has(fs, RuleNoGuardrails) == nil {
		t.Error("a namespace with neither guardrail must be flagged")
	}

	inv.Namespaces[0].ResourceQuotas = []string{"compute"}
	if fs := Run(inv, scores, Options{}); has(fs, RuleNoGuardrails) != nil {
		t.Error("a ResourceQuota alone is enough to clear CAP201")
	}
}

func TestOverProvisionedRequestIsDetectedFromUsage(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Replicas: 3, Containers: []model.Container{
		container("app", memReq(units.Gi), memLim(2*units.Gi)),
	}}
	inv, scores := scene(w, 16, 3, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 200 * units.Mi, Samples: 3}

	fs := Run(inv, scores, Options{Divergence: 2, Headroom: 1.25})
	f := has(fs, RuleMemOverProvision)
	if f == nil {
		t.Fatal("a 1Gi request against 200Mi of usage must be flagged")
	}
	if f.Recommendation == nil {
		t.Fatal("an over-provisioning finding must carry a recommendation")
	}
	if want := 250 * units.Mi; f.Recommendation.Proposed != want {
		t.Errorf("proposed = %d, want %d (200Mi * 1.25)", f.Recommendation.Proposed, want)
	}
}

func TestNoUsageFindingsWithoutMetrics(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Containers: []model.Container{
		container("app", memReq(units.Gi), memLim(2*units.Gi)),
	}}
	inv, scores := scene(w, 16, 3, false)
	inv.MetricsAvailable = false
	inv.Usage[w.Ref] = model.Usage{MemBytes: 200 * units.Mi}

	if fs := Run(inv, scores, Options{}); has(fs, RuleMemOverProvision) != nil {
		t.Error("capsize must not claim over-provisioning it could not observe")
	}
}

func TestUnderProvisionedRequestIsFlagged(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Containers: []model.Container{
		container("app", memReq(128*units.Mi), memLim(2*units.Gi)),
	}}
	inv, scores := scene(w, 16, 3, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 900 * units.Mi, Samples: 1}

	fs := Run(inv, scores, Options{})
	if has(fs, RuleMemUnderProvison) == nil {
		t.Fatal("usage above the request must be flagged; it is unreserved memory")
	}
}

// The headline case: unbounded workload, request far above usage. The cost
// fix is real and so is the harm.
func TestContradictionIsRaisedWhenShrinkingAnUnboundedRequest(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Replicas: 4, Containers: []model.Container{
		container("app", memReq(units.Gi)), // request, no limit
	}}
	inv, scores := scene(w, 16, 7, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 200 * units.Mi, Samples: 4}

	fs := Run(inv, scores, Options{Divergence: 2, Headroom: 1.25})

	c := has(fs, RuleContradiction)
	if c == nil {
		t.Fatal("shrinking the request of an unbounded workload must raise a contradiction")
	}
	if c.Severity != SeverityCritical {
		t.Errorf("contradiction severity = %s, want critical", c.Severity)
	}
	if c.Recommendation.RiskAfter <= c.Recommendation.RiskBefore {
		t.Errorf("risk should rise: %v -> %v", c.Recommendation.RiskBefore, c.Recommendation.RiskAfter)
	}
	if c.Recommendation.Guard == "" {
		t.Error("a contradiction must name the change that has to land first")
	}
	if !strings.Contains(c.Recommendation.Guard, "memory limit") {
		t.Errorf("the guard for an unbounded workload is a limit, got %q", c.Recommendation.Guard)
	}
	for _, want := range []string{"1Gi", "250Mi", "blast radius", "node-1"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail is missing %q; it must state the numbers: %s", want, c.Detail)
		}
	}
}

// The other half of being useful: not crying wolf when the fix is safe.
func TestNoContradictionWhenTheLimitAlreadyBoundsTheWorkload(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Replicas: 4, Containers: []model.Container{
		container("app", memReq(300*units.Mi), memLim(320*units.Mi)),
	}}
	inv, scores := scene(w, 16, 7, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 100 * units.Mi, Samples: 4}

	fs := Run(inv, scores, Options{Divergence: 2, Headroom: 1.25})
	if has(fs, RuleMemOverProvision) == nil {
		t.Fatal("the over-provisioning finding should still be raised")
	}
	// A tight limit means shrinking the request does widen the request/limit
	// gap, so capsize does flag it - but the guard is to lower the limit, not
	// to invent one.
	if c := has(fs, RuleContradiction); c != nil {
		if strings.Contains(c.Recommendation.Guard, "set a memory limit") {
			t.Errorf("a bounded workload must not be told to set a limit it already has: %q", c.Recommendation.Guard)
		}
	}
}

func TestSoleTenantProducesNoContradiction(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Containers: []model.Container{
		container("app", memReq(units.Gi)),
	}}
	inv, scores := scene(w, 16, 0, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 100 * units.Mi, Samples: 1}

	fs := Run(inv, scores, Options{})
	if has(fs, RuleContradiction) != nil {
		t.Error("a workload alone on its node has no neighbors to endanger")
	}
}

func TestFindingsSortWorstFirst(t *testing.T) {
	fs := []Finding{
		{Rule: "a", Severity: SeverityInfo, Risk: 900},
		{Rule: "b", Severity: SeverityCritical, Risk: 1},
		{Rule: "c", Severity: SeverityWarn, Risk: 50},
		{Rule: "d", Severity: SeverityCritical, Risk: 80},
	}
	Sort(fs)
	got := []string{fs[0].Rule, fs[1].Rule, fs[2].Rule, fs[3].Rule}
	want := []string{"d", "b", "c", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if w, ok := Worst(fs); !ok || w != SeverityCritical {
		t.Fatalf("Worst = %v %v", w, ok)
	}
}

func TestSeverityGate(t *testing.T) {
	if !AtLeast(SeverityCritical, SeverityWarn) {
		t.Error("critical must satisfy --fail-on warn")
	}
	if AtLeast(SeverityInfo, SeverityWarn) {
		t.Error("info must not satisfy --fail-on warn")
	}
	if _, ok := ParseSeverity("none"); ok {
		t.Error(`"none" is not a severity; it means the gate is off`)
	}
}
