package model

import "strings"

// systemNamespaces are namespaces whose workloads belong to whoever operates
// the cluster rather than to whoever is reading a capsize report.
//
// This is a name list, and it is a name list on purpose. There is no reliable
// metadata signal for "the provider installed this": on kind,
// local-path-storage carries no distinguishing label, no priorityClassName,
// no addon annotation and no owner reference that says so. Inferring it would
// mean guessing, and guessing wrong in this direction hides a workload the
// user is responsible for. A name list is narrower, but a reader can check it
// against this file and --include-system undoes it in full.
var systemNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,

	// Provider-installed addons that ship in their own namespace.
	"local-path-storage": true, // kind's default storage provisioner
}

// SystemNamespace reports whether ns is operated by the cluster provider.
//
// The kube- prefix is reserved by Kubernetes for system namespaces - the API
// server warns when you create one - so it is treated as a system marker in
// its own right. That covers the distribution-specific cases this list does
// not name (kube-flannel, kube-ovn, and so on).
func SystemNamespace(ns string) bool {
	return systemNamespaces[ns] || strings.HasPrefix(ns, "kube-")
}

// HideSystem drops provider-owned workloads and namespaces from the report.
//
// It is deliberately a post-scoring operation. Blast radius is a property of
// a node, not of a namespace: a kube-system neighbor will OOM you exactly as
// dead as one of your own, so neighbor counts are read cluster-wide and
// scores are computed against the complete inventory before this is called.
// Nothing here touches Node.Tenants, which is where those counts live.
//
// It returns what was removed so the caller can say so. A hidden count the
// reader cannot see is indistinguishable from a clean scan.
func (inv *Inventory) HideSystem() (workloads, namespaces int) {
	keptWorkloads := inv.Workloads[:0]
	for _, w := range inv.Workloads {
		if SystemNamespace(w.Ref.Namespace) {
			workloads++
			continue
		}
		keptWorkloads = append(keptWorkloads, w)
	}
	inv.Workloads = keptWorkloads

	keptNamespaces := inv.Namespaces[:0]
	for _, ns := range inv.Namespaces {
		if SystemNamespace(ns.Name) {
			namespaces++
			continue
		}
		keptNamespaces = append(keptNamespaces, ns)
	}
	inv.Namespaces = keptNamespaces

	return workloads, namespaces
}
