package model

import "testing"

func TestSystemNamespaceClassification(t *testing.T) {
	cases := map[string]bool{
		"kube-system":        true,
		"kube-public":        true,
		"kube-node-lease":    true,
		"local-path-storage": true,
		// The kube- prefix is reserved by Kubernetes for system namespaces.
		"kube-flannel": true,
		"kube-ovn":     true,

		// Everything else belongs to whoever is reading the report.
		"prod":          false,
		"sandbox":       false,
		"batch":         false,
		"default":       false,
		"kubernetes":    false, // no hyphen: not the reserved prefix
		"my-kube-thing": false, // prefix, not substring
		"":              false, // cluster-wide scope must never look systemic
	}
	for ns, want := range cases {
		if got := SystemNamespace(ns); got != want {
			t.Errorf("SystemNamespace(%q) = %v, want %v", ns, got, want)
		}
	}
}

func ref(ns, name string) Ref {
	return Ref{Kind: KindDeployment, Namespace: ns, Name: name}
}

func inventoryWithSystemNeighbors() *Inventory {
	mine := ref("prod", "api")
	sys1 := ref("kube-system", "coredns")
	sys2 := ref("kube-system", "kube-proxy")
	sys3 := ref("local-path-storage", "local-path-provisioner")

	node := &Node{
		Name: "n1", AllocatableMem: 16 << 30,
		Tenants: map[Ref]bool{mine: true, sys1: true, sys2: true, sys3: true},
	}
	wl := func(r Ref) *Workload {
		return &Workload{Ref: r, Replicas: 1, Nodes: []string{"n1"}, RunningPods: 1,
			Containers: []Container{{Name: "app", HasMemRequest: true, MemRequest: 256 << 20}}}
	}
	return &Inventory{
		Nodes:     []*Node{node},
		Workloads: []*Workload{wl(mine), wl(sys1), wl(sys2), wl(sys3)},
		Namespaces: []*Namespace{
			{Name: "prod", Workloads: 1},
			{Name: "kube-system", Workloads: 2},
			{Name: "kube-public", Workloads: 0},
			{Name: "local-path-storage", Workloads: 1},
		},
		Usage: map[Ref]Usage{},
	}
}

func TestHideSystemRemovesReportingScopeOnly(t *testing.T) {
	inv := inventoryWithSystemNeighbors()
	mine := ref("prod", "api")

	before := inv.Nodes[0].Neighbors(mine)
	if before != 3 {
		t.Fatalf("precondition: want 3 neighbors, got %d", before)
	}

	gotW, gotN := inv.HideSystem()
	if gotW != 3 {
		t.Errorf("hid %d workloads, want 3", gotW)
	}
	if gotN != 3 {
		t.Errorf("hid %d namespaces, want 3 (kube-system, kube-public, local-path-storage)", gotN)
	}

	if len(inv.Workloads) != 1 || inv.Workloads[0].Ref != mine {
		t.Fatalf("want only prod/api left, got %+v", inv.Workloads)
	}
	if len(inv.Namespaces) != 1 || inv.Namespaces[0].Name != "prod" {
		t.Fatalf("want only prod left, got %+v", inv.Namespaces)
	}

	// The point of the whole change: tenancy is untouched.
	if after := inv.Nodes[0].Neighbors(mine); after != before {
		t.Fatalf("neighbors changed from %d to %d; a kube-system pod OOMs you "+
			"just as dead whether or not you wanted to read about it", before, after)
	}
	if len(inv.Nodes[0].Tenants) != 4 {
		t.Fatalf("node tenancy was mutated: %d tenants, want 4", len(inv.Nodes[0].Tenants))
	}
}

func TestHideSystemOnAllUserNamespacesHidesNothing(t *testing.T) {
	inv := &Inventory{
		Workloads:  []*Workload{{Ref: ref("prod", "api")}, {Ref: ref("sandbox", "worker")}},
		Namespaces: []*Namespace{{Name: "prod"}, {Name: "sandbox"}},
	}
	if w, n := inv.HideSystem(); w != 0 || n != 0 {
		t.Fatalf("hid %d workloads and %d namespaces from an all-user cluster", w, n)
	}
	if len(inv.Workloads) != 2 || len(inv.Namespaces) != 2 {
		t.Fatal("HideSystem removed user workloads")
	}
}
