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

	// The Has* flags describe EFFECTIVE resources, after Effective() has
	// applied Kubernetes' own defaulting. HasMemRequest true with
	// MemRequestDefaulted true means "the manifest declared no memory
	// request, and the cluster supplied one equal to the limit".
	HasCPURequest bool `json:"hasCpuRequest"`
	HasMemRequest bool `json:"hasMemRequest"`
	HasCPULimit   bool `json:"hasCpuLimit"`
	HasMemLimit   bool `json:"hasMemLimit"`

	CPURequestDefaulted bool `json:"cpuRequestDefaulted,omitempty"`
	MemRequestDefaulted bool `json:"memRequestDefaulted,omitempty"`
}

// Effective applies the defaulting rule the Kubernetes core API applies at
// admission: when a container declares a limit for a resource but no request,
// the request is set equal to the limit.
//
// This is core API behaviour, not a LimitRange, so it happens on every
// cluster with no admission plugin involved and it is invisible in the
// PodTemplateSpec. Reading the template verbatim therefore sees "no request"
// where the scheduler sees a full reservation - and, for a container whose
// requests and limits match on both resources, a Guaranteed pod: the least
// evictable class there is, not the most.
//
// Every model.Container that reaches scoring must have been through here.
func (c Container) Effective() Container {
	if !c.HasMemRequest && c.HasMemLimit {
		c.HasMemRequest, c.MemRequest, c.MemRequestDefaulted = true, c.MemLimit, true
	}
	if !c.HasCPURequest && c.HasCPULimit {
		c.HasCPURequest, c.CPURequest, c.CPURequestDefaulted = true, c.CPULimit, true
	}
	return c
}

// Declares reports whether the manifest itself asked for anything at all.
// A container whose only requests were defaulted from its limits still
// declares something; one with nothing at all is the BestEffort case.
func (c Container) Declares() bool {
	return c.HasCPULimit || c.HasMemLimit ||
		(c.HasCPURequest && !c.CPURequestDefaulted) ||
		(c.HasMemRequest && !c.MemRequestDefaulted)
}

// guaranteed reports whether this container alone satisfies the Guaranteed
// conditions: both resources bounded, with request equal to limit.
func (c Container) guaranteed() bool {
	return c.HasCPURequest && c.HasCPULimit && c.CPURequest == c.CPULimit &&
		c.HasMemRequest && c.HasMemLimit && c.MemRequest == c.MemLimit
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

// QoSClass is the quality-of-service class the kubelet assigns a pod, which
// decides the order things are evicted in when a node runs short.
type QoSClass string

const (
	QoSGuaranteed QoSClass = "Guaranteed"
	QoSBurstable  QoSClass = "Burstable"
	QoSBestEffort QoSClass = "BestEffort"
)

// QoS computes the class from effective resources.
//
// Guaranteed requires every container to bound both CPU and memory with
// request equal to limit. BestEffort requires every container to declare
// nothing at all - it is the absence of both requests and limits, which is
// why a container that sets only limits is emphatically not BestEffort.
// Everything else is Burstable.
//
// capsize considers regular containers and native sidecars, which is what it
// collects; plain init containers do not affect the class in practice because
// they have exited by the time eviction matters.
func (w *Workload) QoS() QoSClass {
	if len(w.Containers) == 0 {
		return QoSBestEffort
	}
	guaranteed, declares := true, false
	for _, c := range w.Containers {
		if !c.guaranteed() {
			guaranteed = false
		}
		if c.Declares() {
			declares = true
		}
	}
	switch {
	case guaranteed:
		return QoSGuaranteed
	case !declares:
		return QoSBestEffort
	default:
		return QoSBurstable
	}
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
