// Package model holds capsize's view of a cluster after collection: plain
// data, no API types beyond the resource quantities, so that detection and
// scoring can be unit-tested without a cluster.
package model

import "fmt"

// Kind is the workload kind capsize attributes a pod to.
type Kind string

const (
	KindDeployment  Kind = "Deployment"
	KindStatefulSet Kind = "StatefulSet"
	KindDaemonSet   Kind = "DaemonSet"
	KindCronJob     Kind = "CronJob"
	KindJob         Kind = "Job"
	KindPod         Kind = "Pod" // a pod with no controller
)

// Ref identifies a workload uniquely across the cluster.
type Ref struct {
	Kind      Kind   `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (r Ref) String() string { return fmt.Sprintf("%s/%s/%s", r.Namespace, r.Kind, r.Name) }

// Short renders "ns/name" for table output, where the kind has its own column.
func (r Ref) Short() string { return r.Namespace + "/" + r.Name }

// Container is one container's declared resource shape. CPU is millicores,
// memory is bytes. The Has* flags distinguish "declared as zero" from
// "not declared", which is the entire subject of half of capsize's findings.
type Container struct {
	Name string `json:"name"`

	// Sidecar marks a native sidecar (an initContainer with restartPolicy
	// Always), which counts toward the pod's scheduled request like a regular
	// container does.
	Sidecar bool `json:"sidecar,omitempty"`

	CPURequest int64 `json:"cpuRequestMilli"`
	MemRequest int64 `json:"memRequestBytes"`
	CPULimit   int64 `json:"cpuLimitMilli"`
	MemLimit   int64 `json:"memLimitBytes"`

	HasCPURequest bool `json:"hasCpuRequest"`
	HasMemRequest bool `json:"hasMemRequest"`
	HasCPULimit   bool `json:"hasCpuLimit"`
	HasMemLimit   bool `json:"hasMemLimit"`
}

// Workload is one controller (or one uncontrolled pod) and its declared shape.
type Workload struct {
	Ref        Ref         `json:"ref"`
	Replicas   int32       `json:"replicas"`
	Containers []Container `json:"containers"`

	// Nodes are the nodes currently hosting this workload's pods. Empty for a
	// workload with no running pods (scaled to zero, a suspended CronJob),
	// which capsize reports but cannot blast-radius score.
	Nodes []string `json:"nodes"`

	// RunningPods is how many pods backed the container shape above.
	RunningPods int `json:"runningPods"`
}

// MemRequest is the memory a scheduler reserves for one pod of this workload.
func (w *Workload) MemRequest() int64 {
	var total int64
	for _, c := range w.Containers {
		total += c.MemRequest
	}
	return total
}

// MemLimit returns the pod's total memory limit and whether every container
// actually declared one. A single unbounded container makes the whole pod
// unbounded for blast-radius purposes, because that container alone can walk
// the node's memory to zero.
func (w *Workload) MemLimit() (total int64, bounded bool) {
	bounded = len(w.Containers) > 0
	for _, c := range w.Containers {
		if !c.HasMemLimit {
			return 0, false
		}
		total += c.MemLimit
	}
	return total, bounded
}

// CPURequest is the CPU a scheduler reserves for one pod, in millicores.
func (w *Workload) CPURequest() int64 {
	var total int64
	for _, c := range w.Containers {
		total += c.CPURequest
	}
	return total
}

// Node is a schedulable node, its capacity, and who else is on it.
type Node struct {
	Name           string `json:"name"`
	AllocatableMem int64  `json:"allocatableMemBytes"`
	AllocatableCPU int64  `json:"allocatableCpuMilli"`

	// Spot is true when any recognised capacity-type label marks this node as
	// interruptible. SpotEvidence records which label proved it, so the finding
	// can be argued with.
	Spot         bool   `json:"spot"`
	SpotEvidence string `json:"spotEvidence,omitempty"`

	// Tenants is the set of distinct workloads with at least one pod here.
	Tenants map[Ref]bool `json:"-"`
}

// Neighbours counts the distinct workloads on this node other than self.
func (n *Node) Neighbours(self Ref) int {
	count := 0
	for ref := range n.Tenants {
		if ref != self {
			count++
		}
	}
	return count
}

// Usage is observed consumption from metrics-server. Because metrics-server
// serves an instantaneous sample rather than a history, capsize takes the
// worst pod in the workload rather than the mean: recommending a request from
// the quietest replica is how you get a rightsizing that pages someone.
type Usage struct {
	MemBytes  int64 `json:"memBytes"`
	CPUMillis int64 `json:"cpuMillis"`
	Samples   int   `json:"samples"` // pods that reported
}

// Namespace records the guardrails a namespace does or does not have.
type Namespace struct {
	Name           string   `json:"name"`
	LimitRanges    []string `json:"limitRanges"`
	ResourceQuotas []string `json:"resourceQuotas"`
	Workloads      int      `json:"workloads"`
}

// Ungoverned reports a namespace that constrains nothing: any pod admitted
// here may request nothing and consume everything.
func (n *Namespace) Ungoverned() bool {
	return len(n.LimitRanges) == 0 && len(n.ResourceQuotas) == 0
}

// Inventory is everything one scan read from the cluster.
type Inventory struct {
	Context    string       `json:"context"`
	Scope      string       `json:"scope"` // "all namespaces" or a namespace name
	Nodes      []*Node      `json:"nodes"`
	Workloads  []*Workload  `json:"workloads"`
	Namespaces []*Namespace `json:"namespaces"`

	// Usage is keyed by workload; absent when metrics were unavailable.
	Usage map[Ref]Usage `json:"-"`

	MetricsAvailable bool   `json:"metricsAvailable"`
	MetricsNote      string `json:"metricsNote,omitempty"`
}

// NodeByName is a lookup helper for the scorer.
func (inv *Inventory) NodeByName(name string) *Node {
	for _, n := range inv.Nodes {
		if n.Name == name {
			return n
		}
	}
	return nil
}
