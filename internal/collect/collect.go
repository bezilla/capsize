// Package collect turns a cluster into a model.Inventory using nothing but
// LIST calls. It degrades rather than fails: a caller who cannot list nodes
// still gets the cost half of the report, with the gap stated plainly.
package collect

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/bezilla/capsize/internal/model"
)

// Reader is the read-only slice of the cluster collect needs. *k8s.Client
// satisfies it; a fake satisfies it in tests. Note that it contains no verb
// that could mutate anything - that is the point.
type Reader interface {
	Nodes(ctx context.Context) ([]corev1.Node, error)
	Namespaces(ctx context.Context) ([]corev1.Namespace, error)
	Pods(ctx context.Context, ns string) ([]corev1.Pod, error)
	Deployments(ctx context.Context, ns string) ([]appsv1.Deployment, error)
	StatefulSets(ctx context.Context, ns string) ([]appsv1.StatefulSet, error)
	DaemonSets(ctx context.Context, ns string) ([]appsv1.DaemonSet, error)
	ReplicaSets(ctx context.Context, ns string) ([]appsv1.ReplicaSet, error)
	Jobs(ctx context.Context, ns string) ([]batchv1.Job, error)
	CronJobs(ctx context.Context, ns string) ([]batchv1.CronJob, error)
	LimitRanges(ctx context.Context, ns string) ([]corev1.LimitRange, error)
	ResourceQuotas(ctx context.Context, ns string) ([]corev1.ResourceQuota, error)
	MetricsAvailable(ctx context.Context) (bool, string)
	PodMetrics(ctx context.Context, ns string) ([]metricsapi.PodMetrics, error)
}

// Options controls the scope of a collection.
type Options struct {
	// Namespace is the namespace to score. Empty means every namespace.
	Namespace string
	// WithMetrics enables the metrics-server probe.
	WithMetrics bool
	// Context is the kubeconfig context name, carried through for display.
	Context string
}

// spotLabels are the node labels that mark interruptible capacity. Values are
// compared case-insensitively because the three major sources disagree on
// case. To support another provider, add a row here; nothing else changes.
var spotLabels = []struct{ Key, Value string }{
	{"karpenter.sh/capacity-type", "spot"},
	{"eks.amazonaws.com/capacityType", "spot"},
	{"node.kubernetes.io/lifecycle", "spot"},
}

// Collect reads the cluster and returns the inventory plus any non-fatal
// warnings the report should surface.
func Collect(ctx context.Context, r Reader, o Options) (*model.Inventory, []string, error) {
	inv := &model.Inventory{
		Context: o.Context,
		Scope:   scopeLabel(o.Namespace),
		Usage:   map[model.Ref]model.Usage{},
	}
	var warnings []string

	// --- nodes ------------------------------------------------------------
	// Nodes are cluster-scoped. Without them there is no ceiling and no
	// neighbor count, so blast radius is simply unavailable.
	nodes, err := r.Nodes(ctx)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf(
			"cannot list nodes (%v) - blast-radius scoring is disabled, cost findings only", rootCause(err)))
	}
	byName := map[string]*model.Node{}
	for i := range nodes {
		n := &nodes[i]
		mem := quantity(n.Status.Allocatable, corev1.ResourceMemory)
		cpu := milliQuantity(n.Status.Allocatable, corev1.ResourceCPU)
		spot, evidence := detectSpot(n.Labels)
		mn := &model.Node{
			Name:           n.Name,
			AllocatableMem: mem,
			AllocatableCPU: cpu,
			Spot:           spot,
			SpotEvidence:   evidence,
			Tenants:        map[model.Ref]bool{},
		}
		byName[n.Name] = mn
		inv.Nodes = append(inv.Nodes, mn)
	}

	// --- pods -------------------------------------------------------------
	// Tenancy is always read cluster-wide when permitted, even for a
	// single-namespace scan: a neighbor in kube-system will OOM just as
	// dead as one in your namespace. If that read is denied, neighbor
	// counts become a lower bound and we say so.
	tenancyPods, err := r.Pods(ctx, "")
	tenancyClusterWide := err == nil
	if err != nil {
		if o.Namespace == "" {
			return nil, warnings, fmt.Errorf("listing pods: %w", err)
		}
		warnings = append(warnings, fmt.Sprintf(
			"cannot list pods cluster-wide (%v) - neighbor counts are a lower bound "+
				"and risk scores are therefore understated", rootCause(err)))
		tenancyPods, err = r.Pods(ctx, o.Namespace)
		if err != nil {
			return nil, warnings, fmt.Errorf("listing pods in %s: %w", o.Namespace, err)
		}
	}

	// Owner-reference chains: Pod -> ReplicaSet -> Deployment, Pod -> Job ->
	// CronJob. Both intermediate kinds are read cluster-wide alongside the
	// pods so the chain resolves for tenancy as well as for scope.
	ownerNS := ""
	if !tenancyClusterWide {
		ownerNS = o.Namespace
	}
	rsOwner := map[string]string{}  // "ns/rs"  -> deployment name
	jobOwner := map[string]string{} // "ns/job" -> cronjob name
	if rss, err := r.ReplicaSets(ctx, ownerNS); err == nil {
		for i := range rss {
			if c := controllerOf(rss[i].OwnerReferences); c != nil && c.Kind == "Deployment" {
				rsOwner[rss[i].Namespace+"/"+rss[i].Name] = c.Name
			}
		}
	} else {
		warnings = append(warnings, fmt.Sprintf(
			"cannot list replicasets (%v) - pods will be attributed to their replicaset "+
				"rather than their deployment", rootCause(err)))
	}
	if jobs, err := r.Jobs(ctx, ownerNS); err == nil {
		for i := range jobs {
			if c := controllerOf(jobs[i].OwnerReferences); c != nil && c.Kind == "CronJob" {
				jobOwner[jobs[i].Namespace+"/"+jobs[i].Name] = c.Name
			}
		}
	}

	podRef := map[string]model.Ref{} // "ns/pod" -> owning workload
	nodesFor := map[model.Ref]map[string]bool{}
	podsFor := map[model.Ref]int{}
	orphans := map[model.Ref]*corev1.Pod{}

	for i := range tenancyPods {
		p := &tenancyPods[i]
		if isTerminal(p) {
			continue
		}
		ref := resolveOwner(p, rsOwner, jobOwner)
		podRef[p.Namespace+"/"+p.Name] = ref
		podsFor[ref]++

		if p.Spec.NodeName != "" {
			if n, ok := byName[p.Spec.NodeName]; ok {
				n.Tenants[ref] = true
			}
			if nodesFor[ref] == nil {
				nodesFor[ref] = map[string]bool{}
			}
			nodesFor[ref][p.Spec.NodeName] = true
		}
		if ref.Kind == model.KindPod || ref.Kind == model.KindJob {
			if _, seen := orphans[ref]; !seen {
				orphans[ref] = p
			}
		}
	}

	// --- workloads --------------------------------------------------------
	// Enumerated from the controllers rather than from the pods, so a
	// deployment scaled to zero - a very common source of a bad request
	// template that nobody notices until it scales up - is still scored.
	inScope := func(ns string) bool { return o.Namespace == "" || ns == o.Namespace }

	if deps, err := r.Deployments(ctx, o.Namespace); err == nil {
		for i := range deps {
			d := &deps[i]
			inv.Workloads = append(inv.Workloads, workload(
				model.Ref{Kind: model.KindDeployment, Namespace: d.Namespace, Name: d.Name},
				replicas(d.Spec.Replicas), d.Spec.Template.Spec, nodesFor, podsFor))
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot list deployments (%v)", rootCause(err)))
	}
	if sets, err := r.StatefulSets(ctx, o.Namespace); err == nil {
		for i := range sets {
			s := &sets[i]
			inv.Workloads = append(inv.Workloads, workload(
				model.Ref{Kind: model.KindStatefulSet, Namespace: s.Namespace, Name: s.Name},
				replicas(s.Spec.Replicas), s.Spec.Template.Spec, nodesFor, podsFor))
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot list statefulsets (%v)", rootCause(err)))
	}
	if sets, err := r.DaemonSets(ctx, o.Namespace); err == nil {
		for i := range sets {
			d := &sets[i]
			inv.Workloads = append(inv.Workloads, workload(
				model.Ref{Kind: model.KindDaemonSet, Namespace: d.Namespace, Name: d.Name},
				d.Status.DesiredNumberScheduled, d.Spec.Template.Spec, nodesFor, podsFor))
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot list daemonsets (%v)", rootCause(err)))
	}
	if cjs, err := r.CronJobs(ctx, o.Namespace); err == nil {
		for i := range cjs {
			c := &cjs[i]
			inv.Workloads = append(inv.Workloads, workload(
				model.Ref{Kind: model.KindCronJob, Namespace: c.Namespace, Name: c.Name},
				1, c.Spec.JobTemplate.Spec.Template.Spec, nodesFor, podsFor))
		}
	}

	// Uncontrolled pods and standalone Jobs have no template to read, so their
	// own spec is the template.
	for ref, p := range orphans {
		if !inScope(ref.Namespace) {
			continue
		}
		inv.Workloads = append(inv.Workloads, workload(ref, 1, p.Spec, nodesFor, podsFor))
	}

	sort.Slice(inv.Workloads, func(i, j int) bool {
		a, b := inv.Workloads[i].Ref, inv.Workloads[j].Ref
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})

	// --- namespace guardrails --------------------------------------------
	nsNames, nsWarn := namespaceNames(ctx, r, o.Namespace, inv.Workloads)
	warnings = append(warnings, nsWarn...)
	counts := map[string]int{}
	for _, w := range inv.Workloads {
		counts[w.Ref.Namespace]++
	}
	for _, name := range nsNames {
		ns := &model.Namespace{Name: name, Workloads: counts[name]}
		if lrs, err := r.LimitRanges(ctx, name); err == nil {
			for i := range lrs {
				ns.LimitRanges = append(ns.LimitRanges, lrs[i].Name)
			}
		}
		if rqs, err := r.ResourceQuotas(ctx, name); err == nil {
			for i := range rqs {
				ns.ResourceQuotas = append(ns.ResourceQuotas, rqs[i].Name)
			}
		}
		inv.Namespaces = append(inv.Namespaces, ns)
	}

	// --- observed usage ---------------------------------------------------
	if !o.WithMetrics {
		inv.MetricsNote = "skipped (--no-metrics)"
	} else if ok, why := r.MetricsAvailable(ctx); !ok {
		inv.MetricsNote = why
	} else if pms, err := r.PodMetrics(ctx, o.Namespace); err != nil {
		inv.MetricsNote = fmt.Sprintf("metrics-server is registered but the read failed: %v", rootCause(err))
	} else {
		inv.MetricsAvailable = true
		for i := range pms {
			pm := &pms[i]
			ref, known := podRef[pm.Namespace+"/"+pm.Name]
			if !known {
				continue
			}
			var mem, cpu int64
			for _, c := range pm.Containers {
				mem += quantity(c.Usage, corev1.ResourceMemory)
				cpu += milliQuantity(c.Usage, corev1.ResourceCPU)
			}
			// Worst pod wins: see the doc comment on model.Usage.
			u := inv.Usage[ref]
			if mem > u.MemBytes {
				u.MemBytes = mem
			}
			if cpu > u.CPUMillis {
				u.CPUMillis = cpu
			}
			u.Samples++
			inv.Usage[ref] = u
		}
		if len(inv.Usage) == 0 {
			inv.MetricsNote = "metrics-server returned no samples for the workloads in scope"
		}
	}

	return inv, warnings, nil
}

// --- helpers --------------------------------------------------------------

func scopeLabel(ns string) string {
	if ns == "" {
		return "all namespaces"
	}
	return "namespace " + ns
}

func namespaceNames(ctx context.Context, r Reader, scope string, ws []*model.Workload) ([]string, []string) {
	if scope != "" {
		return []string{scope}, nil
	}
	var warnings []string
	if nss, err := r.Namespaces(ctx); err == nil {
		out := make([]string, 0, len(nss))
		for i := range nss {
			out = append(out, nss[i].Name)
		}
		sort.Strings(out)
		return out, nil
	} else {
		warnings = append(warnings, fmt.Sprintf(
			"cannot list namespaces (%v) - guardrail checks cover only namespaces "+
				"that already contain a visible workload", rootCause(err)))
	}
	seen := map[string]bool{}
	var out []string
	for _, w := range ws {
		if !seen[w.Ref.Namespace] {
			seen[w.Ref.Namespace] = true
			out = append(out, w.Ref.Namespace)
		}
	}
	sort.Strings(out)
	return out, warnings
}

func workload(ref model.Ref, reps int32, spec corev1.PodSpec, nodesFor map[model.Ref]map[string]bool, podsFor map[model.Ref]int) *model.Workload {
	w := &model.Workload{
		Ref:         ref,
		Replicas:    reps,
		Containers:  containersOf(spec),
		RunningPods: podsFor[ref],
	}
	for n := range nodesFor[ref] {
		w.Nodes = append(w.Nodes, n)
	}
	sort.Strings(w.Nodes)
	return w
}

// containersOf flattens a pod spec into capsize's container view. Native
// sidecars (initContainers with restartPolicy Always) are included because
// the scheduler counts their requests for the life of the pod; ordinary init
// containers are not, because they do not run alongside the app.
func containersOf(spec corev1.PodSpec) []model.Container {
	out := make([]model.Container, 0, len(spec.Containers))
	for i := range spec.Containers {
		out = append(out, containerOf(&spec.Containers[i], false))
	}
	for i := range spec.InitContainers {
		ic := &spec.InitContainers[i]
		if ic.RestartPolicy != nil && *ic.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			out = append(out, containerOf(ic, true))
		}
	}
	return out
}

func containerOf(c *corev1.Container, sidecar bool) model.Container {
	mc := model.Container{Name: c.Name, Sidecar: sidecar}
	req, lim := c.Resources.Requests, c.Resources.Limits

	if q, ok := req[corev1.ResourceCPU]; ok {
		mc.HasCPURequest, mc.CPURequest = true, q.MilliValue()
	}
	if q, ok := req[corev1.ResourceMemory]; ok {
		mc.HasMemRequest, mc.MemRequest = true, q.Value()
	}
	if q, ok := lim[corev1.ResourceCPU]; ok {
		mc.HasCPULimit, mc.CPULimit = true, q.MilliValue()
	}
	if q, ok := lim[corev1.ResourceMemory]; ok {
		mc.HasMemLimit, mc.MemLimit = true, q.Value()
	}
	// The PodTemplateSpec records what the author wrote; the cluster schedules
	// what the API defaulted it to. Everything downstream wants the latter.
	return mc.Effective()
}

func detectSpot(labels map[string]string) (bool, string) {
	for _, l := range spotLabels {
		if v, ok := labels[l.Key]; ok && strings.EqualFold(v, l.Value) {
			return true, l.Key + "=" + v
		}
	}
	return false, ""
}

func controllerOf(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

func resolveOwner(p *corev1.Pod, rsOwner, jobOwner map[string]string) model.Ref {
	c := controllerOf(p.OwnerReferences)
	if c == nil {
		return model.Ref{Kind: model.KindPod, Namespace: p.Namespace, Name: p.Name}
	}
	switch c.Kind {
	case "ReplicaSet":
		if dep, ok := rsOwner[p.Namespace+"/"+c.Name]; ok {
			return model.Ref{Kind: model.KindDeployment, Namespace: p.Namespace, Name: dep}
		}
		// The replicaset list was denied or the RS is orphaned; attribute to
		// the pod itself rather than inventing a deployment that may not exist.
		return model.Ref{Kind: model.KindPod, Namespace: p.Namespace, Name: p.Name}
	case "StatefulSet":
		return model.Ref{Kind: model.KindStatefulSet, Namespace: p.Namespace, Name: c.Name}
	case "DaemonSet":
		return model.Ref{Kind: model.KindDaemonSet, Namespace: p.Namespace, Name: c.Name}
	case "Job":
		if cj, ok := jobOwner[p.Namespace+"/"+c.Name]; ok {
			return model.Ref{Kind: model.KindCronJob, Namespace: p.Namespace, Name: cj}
		}
		return model.Ref{Kind: model.KindJob, Namespace: p.Namespace, Name: c.Name}
	default:
		return model.Ref{Kind: model.KindPod, Namespace: p.Namespace, Name: p.Name}
	}
}

// isTerminal filters pods that hold no resources: they neither contribute to
// tenancy nor deserve a finding.
func isTerminal(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

func replicas(p *int32) int32 {
	if p == nil {
		return 1
	}
	return *p
}

func quantity(list corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := list[name]; ok {
		return q.Value()
	}
	return 0
}

func milliQuantity(list corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := list[name]; ok {
		return q.MilliValue()
	}
	return 0
}

// rootCause trims the multi-line RBAC errors the API server returns so a
// warning line stays a line.
func rootCause(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
