package risk

import (
	"testing"

	"github.com/bezilla/capsize/internal/model"
)

// crowdedCluster puts one user workload on a node it shares with three
// provider-owned workloads.
func crowdedCluster() (*model.Inventory, model.Ref) {
	mine := model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: "api"}
	sys := []model.Ref{
		{Kind: model.KindDaemonSet, Namespace: "kube-system", Name: "kube-proxy"},
		{Kind: model.KindDeployment, Namespace: "kube-system", Name: "coredns"},
		{Kind: model.KindDeployment, Namespace: "local-path-storage", Name: "local-path-provisioner"},
	}

	node := &model.Node{Name: "n1", AllocatableMem: 16 * gi, Tenants: map[model.Ref]bool{mine: true}}
	wl := func(r model.Ref) *model.Workload {
		return &model.Workload{Ref: r, Replicas: 1, Nodes: []string{"n1"}, RunningPods: 1,
			Containers: []model.Container{{Name: "app", HasMemRequest: true, MemRequest: 256 * mi}}}
	}
	inv := &model.Inventory{Nodes: []*model.Node{node}, Workloads: []*model.Workload{wl(mine)}}
	for _, r := range sys {
		node.Tenants[r] = true
		inv.Workloads = append(inv.Workloads, wl(r))
	}
	return inv, mine
}

// Narrowing the report must not narrow the arithmetic. Provider-owned
// workloads are unactionable to read about and lethal to sit next to.
func TestHidingSystemWorkloadsDoesNotChangeAnyScore(t *testing.T) {
	full, mine := crowdedCluster()
	opts := Options{RequestFloor: 10 * mi}

	before := All(full, opts)

	// The production order: score the complete inventory, then narrow.
	full.HideSystem()
	after := All(full, opts)

	if len(after) != 1 {
		t.Fatalf("want 1 workload left, got %d", len(after))
	}
	b, a := before[mine], after[mine]

	if a.Risk != b.Risk {
		t.Errorf("risk moved from %v to %v when system workloads were hidden", b.Risk, a.Risk)
	}
	if a.Neighbours != b.Neighbours {
		t.Errorf("neighbours moved from %d to %d; tenancy must stay cluster-wide", b.Neighbours, a.Neighbours)
	}
	if a.Ratio != b.Ratio || a.Ceiling != b.Ceiling || a.Node != b.Node {
		t.Errorf("score inputs moved:\n before %+v\n after  %+v", b, a)
	}
	if a.Neighbours != 3 {
		t.Errorf("neighbours = %d, want 3 provider-owned tenants", a.Neighbours)
	}
}

// The test above only means something if the mistake it guards against would
// actually show up. This proves it would: filtering tenancy rather than the
// report understates the score badly.
func TestFilteringTenancyWouldUnderstateTheScore(t *testing.T) {
	full, mine := crowdedCluster()
	opts := Options{RequestFloor: 10 * mi}
	correct := Of(full.Workloads[0], full, opts)

	// Simulate the bug: drop system pods from node tenancy before scoring.
	for ref := range full.Nodes[0].Tenants {
		if model.SystemNamespace(ref.Namespace) {
			full.Nodes[0].Tenants[ref] = false
			delete(full.Nodes[0].Tenants, ref)
		}
	}
	wrong := Of(full.Workloads[0], full, opts)

	if wrong.Risk >= correct.Risk {
		t.Fatalf("the guard is toothless: dropping system tenancy gave %v, correct is %v",
			wrong.Risk, correct.Risk)
	}
	if wrong.Neighbours != 0 || correct.Neighbours != 3 {
		t.Fatalf("neighbours: wrong=%d correct=%d", wrong.Neighbours, correct.Neighbours)
	}
	_ = mine
}
