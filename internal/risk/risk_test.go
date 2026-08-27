package risk

import (
	"math"
	"testing"

	"github.com/bezilla/capsize/internal/model"
)

const (
	gi = int64(1) << 30
	mi = int64(1) << 20
)

func nodeWith(name string, allocGi int64, spot bool, tenants ...model.Ref) *model.Node {
	n := &model.Node{
		Name:           name,
		AllocatableMem: allocGi * gi,
		Spot:           spot,
		Tenants:        map[model.Ref]bool{},
	}
	for _, t := range tenants {
		n.Tenants[t] = true
	}
	return n
}

func ref(name string) model.Ref {
	return model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: name}
}

func wl(name string, reqMi, limMi int64, nodes ...string) *model.Workload {
	c := model.Container{Name: "app"}
	if reqMi > 0 {
		c.HasMemRequest, c.MemRequest = true, reqMi*mi
	}
	if limMi > 0 {
		c.HasMemLimit, c.MemLimit = true, limMi*mi
	}
	return &model.Workload{Ref: ref(name), Replicas: 1, Containers: []model.Container{c}, Nodes: nodes, RunningPods: 1}
}

func inventory(nodes ...*model.Node) *model.Inventory {
	return &model.Inventory{Nodes: nodes, Usage: map[model.Ref]model.Usage{}}
}

func approx(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func TestFormulaMatchesTheSpecExactly(t *testing.T) {
	// 16Gi node, 512Mi limit, 256Mi request, 3 neighbors, on-demand.
	// ceiling = min(16Gi, 512Mi) = 512Mi
	// ratio   = 512/256 = 2
	// risk    = 2 * log2(4) * 1.0 = 4
	n := nodeWith("n1", 16, false, ref("a"), ref("b"), ref("c"))
	w := wl("api", 256, 512, "n1")
	n.Tenants[w.Ref] = true

	s := Of(w, inventory(n), Options{RequestFloor: 10 * mi})
	if !s.Scored {
		t.Fatalf("not scored: %s", s.Reason)
	}
	approx(t, "ratio", s.Ratio, 2)
	approx(t, "risk", s.Risk, 4)
	if s.Neighbors != 3 {
		t.Fatalf("neighbors = %d, want 3", s.Neighbors)
	}
	if s.CeilingSource != FromLimit {
		t.Fatalf("ceiling source = %q, want %q", s.CeilingSource, FromLimit)
	}
}

// This is the whole thesis: identical workload, limit removed, score explodes.
func TestUnboundedWorkloadScoresDramaticallyHigher(t *testing.T) {
	tenants := []model.Ref{ref("a"), ref("b"), ref("c")}

	bounded := wl("api", 256, 512, "n1")
	nb := nodeWith("n1", 16, false, tenants...)
	nb.Tenants[bounded.Ref] = true
	sb := Of(bounded, inventory(nb), Options{RequestFloor: 10 * mi})

	unbounded := wl("api", 256, 0, "n1")
	nu := nodeWith("n1", 16, false, tenants...)
	nu.Tenants[unbounded.Ref] = true
	su := Of(unbounded, inventory(nu), Options{RequestFloor: 10 * mi})

	if su.CeilingSource != FromNode {
		t.Fatalf("an unlimited container's ceiling must be the node, got %q", su.CeilingSource)
	}
	// ceiling 16Gi vs 512Mi is a 32x jump in ratio, and so in risk.
	approx(t, "unbounded/bounded", su.Risk/sb.Risk, 32)
	if su.Risk <= sb.Risk {
		t.Fatal("removing a limit must raise the score; the tool's central claim is broken")
	}
}

func TestSpotCapacityMultipliesByOnePointFive(t *testing.T) {
	tenants := []model.Ref{ref("a")}
	w := wl("api", 256, 512, "n1")

	od := nodeWith("n1", 16, false, tenants...)
	od.Tenants[w.Ref] = true
	spot := nodeWith("n1", 16, true, tenants...)
	spot.Tenants[w.Ref] = true

	sod := Of(w, inventory(od), Options{RequestFloor: 10 * mi})
	ssp := Of(w, inventory(spot), Options{RequestFloor: 10 * mi})

	approx(t, "spot/on-demand", ssp.Risk/sod.Risk, SpotFactor)
	if ssp.SpotFactor != SpotFactor {
		t.Fatalf("SpotFactor = %v, want %v", ssp.SpotFactor, SpotFactor)
	}
}

func TestSoleTenantScoresZeroAndSaysSo(t *testing.T) {
	w := wl("api", 256, 0, "n1")
	n := nodeWith("n1", 16, false)
	n.Tenants[w.Ref] = true

	s := Of(w, inventory(n), Options{RequestFloor: 10 * mi})
	if s.Risk != 0 {
		t.Fatalf("risk = %v, want 0: log2(1+0) is zero by definition", s.Risk)
	}
	if !s.SoleTenant() {
		t.Fatal("a zero score with no neighbors must be distinguishable from a safe workload")
	}
}

func TestMissingRequestUsesTheFloorAndFlagsIt(t *testing.T) {
	w := wl("api", 0, 0, "n1")
	n := nodeWith("n1", 16, false, ref("a"))
	n.Tenants[w.Ref] = true

	s := Of(w, inventory(n), Options{RequestFloor: 10 * mi})
	if !s.RequestAssumed {
		t.Fatal("a score built on an assumed request must say so")
	}
	if s.Request != 10*mi {
		t.Fatalf("request = %d, want the floor %d", s.Request, 10*mi)
	}
	if math.IsInf(s.Risk, 0) || math.IsNaN(s.Risk) {
		t.Fatalf("risk must stay finite and JSON-encodable, got %v", s.Risk)
	}
	// 16Gi/10Mi = 1638.4, times log2(2) = 1638.4
	approx(t, "risk", s.Risk, float64(16*gi)/float64(10*mi))
}

func TestWorstNodeWinsWhenPodsAreSpread(t *testing.T) {
	w := wl("api", 256, 512, "quiet", "crowded")
	quiet := nodeWith("quiet", 16, false, ref("a"))
	crowded := nodeWith("crowded", 16, false, ref("a"), ref("b"), ref("c"), ref("d"), ref("e"), ref("f"), ref("g"))
	quiet.Tenants[w.Ref] = true
	crowded.Tenants[w.Ref] = true

	s := Of(w, inventory(quiet, crowded), Options{RequestFloor: 10 * mi})
	if s.Node != "crowded" {
		t.Fatalf("scored against %q; blast radius is a worst-case question", s.Node)
	}
	if s.Neighbors != 7 {
		t.Fatalf("neighbors = %d, want 7", s.Neighbors)
	}
}

func TestWorkloadWithNoPodsIsScoredHypothetically(t *testing.T) {
	w := wl("batch", 256, 0)
	w.RunningPods = 0
	small := nodeWith("small", 4, false, ref("a"))
	big := nodeWith("big", 64, false, ref("a"))

	s := Of(w, inventory(small, big), Options{RequestFloor: 10 * mi})
	if !s.Hypothetical {
		t.Fatal("a workload with no running pods must be flagged as a forecast")
	}
	if s.Node != "big" {
		t.Fatalf("scored against %q, want the roomiest ceiling it could inherit", s.Node)
	}
}

func TestNoNodesMeansNotScoredRatherThanZero(t *testing.T) {
	s := Of(wl("api", 256, 512), inventory(), Options{RequestFloor: 10 * mi})
	if s.Scored {
		t.Fatal("without nodes there is no blast radius to report")
	}
	if s.Reason == "" {
		t.Fatal("an unscored workload must carry a reason")
	}
}

// Project is what makes the contradiction detectable: it prices a proposed
// change in the same arithmetic as the original score.
func TestProjectSharesArithmeticWithTheLiveScore(t *testing.T) {
	w := wl("api", 1024, 0, "n1") // 1Gi request, no limit
	n := nodeWith("n1", 16, false, ref("a"), ref("b"), ref("c"))
	n.Tenants[w.Ref] = true
	s := Of(w, inventory(n), Options{RequestFloor: 10 * mi})

	// The classic cost recommendation: usage is 200Mi, so shrink the request.
	after, _ := s.Project(256*mi, s.Ceiling)
	if after <= s.Risk {
		t.Fatalf("shrinking an unbounded workload's request must raise blast radius: %v -> %v", s.Risk, after)
	}
	approx(t, "projected", after, s.Risk*4) // 1Gi -> 256Mi is 4x the ratio

	// Projecting the current values must reproduce the current score exactly.
	same, _ := s.Project(s.Request, s.Ceiling)
	approx(t, "identity", same, s.Risk)
}
