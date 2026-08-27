package collect

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/pjbezilla/capsize/internal/model"
)

// fakeReader is a hand-rolled Reader. It is deliberately not client-go's fake
// clientset: internal/guard forbids importing client-go outside internal/k8s,
// and a struct of slices is easier to read anyway.
type fakeReader struct {
	nodes      []corev1.Node
	namespaces []corev1.Namespace
	pods       []corev1.Pod
	deploys    []appsv1.Deployment
	stss       []appsv1.StatefulSet
	dss        []appsv1.DaemonSet
	rss        []appsv1.ReplicaSet
	jobs       []batchv1.Job
	cronjobs   []batchv1.CronJob
	limits     []corev1.LimitRange
	quotas     []corev1.ResourceQuota
	metrics    []metricsapi.PodMetrics

	metricsUp  bool
	nodesErr   error
	allPodsErr error
}

func (f *fakeReader) Nodes(context.Context) ([]corev1.Node, error) {
	return f.nodes, f.nodesErr
}
func (f *fakeReader) Namespaces(context.Context) ([]corev1.Namespace, error) {
	return f.namespaces, nil
}
func (f *fakeReader) Pods(_ context.Context, ns string) ([]corev1.Pod, error) {
	if ns == "" && f.allPodsErr != nil {
		return nil, f.allPodsErr
	}
	return filter(f.pods, ns, func(p corev1.Pod) string { return p.Namespace }), nil
}
func (f *fakeReader) Deployments(_ context.Context, ns string) ([]appsv1.Deployment, error) {
	return filter(f.deploys, ns, func(d appsv1.Deployment) string { return d.Namespace }), nil
}
func (f *fakeReader) StatefulSets(_ context.Context, ns string) ([]appsv1.StatefulSet, error) {
	return filter(f.stss, ns, func(s appsv1.StatefulSet) string { return s.Namespace }), nil
}
func (f *fakeReader) DaemonSets(_ context.Context, ns string) ([]appsv1.DaemonSet, error) {
	return filter(f.dss, ns, func(d appsv1.DaemonSet) string { return d.Namespace }), nil
}
func (f *fakeReader) ReplicaSets(_ context.Context, ns string) ([]appsv1.ReplicaSet, error) {
	return filter(f.rss, ns, func(r appsv1.ReplicaSet) string { return r.Namespace }), nil
}
func (f *fakeReader) Jobs(_ context.Context, ns string) ([]batchv1.Job, error) {
	return filter(f.jobs, ns, func(j batchv1.Job) string { return j.Namespace }), nil
}
func (f *fakeReader) CronJobs(_ context.Context, ns string) ([]batchv1.CronJob, error) {
	return filter(f.cronjobs, ns, func(c batchv1.CronJob) string { return c.Namespace }), nil
}
func (f *fakeReader) LimitRanges(_ context.Context, ns string) ([]corev1.LimitRange, error) {
	return filter(f.limits, ns, func(l corev1.LimitRange) string { return l.Namespace }), nil
}
func (f *fakeReader) ResourceQuotas(_ context.Context, ns string) ([]corev1.ResourceQuota, error) {
	return filter(f.quotas, ns, func(q corev1.ResourceQuota) string { return q.Namespace }), nil
}
func (f *fakeReader) MetricsAvailable(context.Context) (bool, string) {
	if f.metricsUp {
		return true, ""
	}
	return false, "metrics-server not installed"
}
func (f *fakeReader) PodMetrics(_ context.Context, ns string) ([]metricsapi.PodMetrics, error) {
	return filter(f.metrics, ns, func(m metricsapi.PodMetrics) string { return m.Namespace }), nil
}

func filter[T any](in []T, ns string, nsOf func(T) string) []T {
	if ns == "" {
		return in
	}
	var out []T
	for _, v := range in {
		if nsOf(v) == ns {
			out = append(out, v)
		}
	}
	return out
}

// --- fixture builders -----------------------------------------------------

func node(name string, memGi int64, labels map[string]string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceMemory: *resource.NewQuantity(memGi<<30, resource.BinarySI),
			corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
		}},
	}
}

func ctr(name, req, lim string) corev1.Container {
	c := corev1.Container{Name: name, Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{},
	}}
	if req != "" {
		c.Resources.Requests[corev1.ResourceMemory] = resource.MustParse(req)
	}
	if lim != "" {
		c.Resources.Limits[corev1.ResourceMemory] = resource.MustParse(lim)
	}
	return c
}

func ctrlRef(kind, name string) metav1.OwnerReference {
	yes := true
	return metav1.OwnerReference{Kind: kind, Name: name, Controller: &yes}
}

func pod(ns, name, nodeName string, owner *metav1.OwnerReference, cs ...corev1.Container) corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{NodeName: nodeName, Containers: cs},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if owner != nil {
		p.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	return p
}

// --- tests ----------------------------------------------------------------

func TestResolvesPodToDeploymentThroughReplicaSet(t *testing.T) {
	rsRef := ctrlRef("ReplicaSet", "api-7d9f")
	f := &fakeReader{
		nodes: []corev1.Node{node("n1", 16, nil)},
		rss: []appsv1.ReplicaSet{{ObjectMeta: metav1.ObjectMeta{
			Namespace: "prod", Name: "api-7d9f",
			OwnerReferences: []metav1.OwnerReference{ctrlRef("Deployment", "api")},
		}}},
		deploys: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "api"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{ctr("api", "256Mi", "512Mi")}},
			}},
		}},
		pods: []corev1.Pod{pod("prod", "api-7d9f-abc", "n1", &rsRef, ctr("api", "256Mi", "512Mi"))},
	}

	inv, _, err := Collect(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Workloads) != 1 {
		t.Fatalf("want 1 workload, got %d: %+v", len(inv.Workloads), inv.Workloads)
	}
	w := inv.Workloads[0]
	if w.Ref.Kind != model.KindDeployment || w.Ref.Name != "api" {
		t.Fatalf("pod was not attributed to its deployment: %s", w.Ref)
	}
	if got := w.Nodes; len(got) != 1 || got[0] != "n1" {
		t.Fatalf("want node n1, got %v", got)
	}
	if !inv.Nodes[0].Tenants[w.Ref] {
		t.Fatal("deployment is not recorded as a tenant of n1")
	}
}

func TestNeighboursExcludeSelfAndCountDistinctWorkloads(t *testing.T) {
	dsRef := ctrlRef("DaemonSet", "logs")
	stsRef := ctrlRef("StatefulSet", "db")
	f := &fakeReader{
		nodes: []corev1.Node{node("n1", 16, nil)},
		pods: []corev1.Pod{
			pod("kube-system", "logs-a", "n1", &dsRef, ctr("l", "64Mi", "64Mi")),
			pod("kube-system", "logs-b", "n1", &dsRef, ctr("l", "64Mi", "64Mi")), // same workload
			pod("prod", "db-0", "n1", &stsRef, ctr("db", "1Gi", "2Gi")),
			pod("prod", "loose", "n1", nil, ctr("x", "", "")),
		},
	}
	inv, _, err := Collect(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	n := inv.Nodes[0]
	self := model.Ref{Kind: model.KindStatefulSet, Namespace: "prod", Name: "db"}
	// Tenants: logs (DaemonSet), db (StatefulSet), loose (Pod) = 3 distinct;
	// two logs pods must collapse to one.
	if got := n.Neighbours(self); got != 2 {
		t.Fatalf("want 2 neighbours of %s, got %d (tenants=%v)", self, got, n.Tenants)
	}
}

func TestSpotDetectionAcrossProviders(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"karpenter", map[string]string{"karpenter.sh/capacity-type": "spot"}, true},
		{"eks-uppercase", map[string]string{"eks.amazonaws.com/capacityType": "SPOT"}, true},
		{"lifecycle", map[string]string{"node.kubernetes.io/lifecycle": "spot"}, true},
		{"on-demand", map[string]string{"karpenter.sh/capacity-type": "on-demand"}, false},
		{"unlabelled", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeReader{nodes: []corev1.Node{node("n1", 8, tc.labels)}}
			inv, _, err := Collect(context.Background(), f, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if inv.Nodes[0].Spot != tc.want {
				t.Fatalf("spot=%v, want %v (evidence %q)", inv.Nodes[0].Spot, tc.want, inv.Nodes[0].SpotEvidence)
			}
		})
	}
}

func TestNativeSidecarCountsTowardRequestButPlainInitDoesNot(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	sidecar := ctr("proxy", "128Mi", "128Mi")
	sidecar.RestartPolicy = &always
	f := &fakeReader{
		deploys: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "api"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers:     []corev1.Container{ctr("api", "256Mi", "512Mi")},
				InitContainers: []corev1.Container{sidecar, ctr("migrate", "1Gi", "1Gi")},
			}}},
		}},
	}
	inv, _, err := Collect(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	w := inv.Workloads[0]
	if len(w.Containers) != 2 {
		t.Fatalf("want app + sidecar, got %d containers", len(w.Containers))
	}
	want := int64(256+128) << 20
	if got := w.MemRequest(); got != want {
		t.Fatalf("MemRequest=%d, want %d (the plain init container must not be counted)", got, want)
	}
}

func TestScaledToZeroWorkloadIsStillCollected(t *testing.T) {
	var zero int32
	f := &fakeReader{
		deploys: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "batch"},
			Spec: appsv1.DeploymentSpec{Replicas: &zero, Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{ctr("b", "", "")}},
			}},
		}},
	}
	inv, _, err := Collect(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Workloads) != 1 || inv.Workloads[0].RunningPods != 0 {
		t.Fatalf("a zero-replica deployment must still be scored: %+v", inv.Workloads)
	}
}

func TestMissingNodePermissionDegradesInsteadOfFailing(t *testing.T) {
	f := &fakeReader{
		nodesErr: errors.New(`nodes is forbidden: User "dev" cannot list resource "nodes"`),
		deploys: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "api"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{ctr("api", "", "")}},
			}},
		}},
	}
	inv, warnings, err := Collect(context.Background(), f, Options{})
	if err != nil {
		t.Fatalf("a denied node list must degrade, not fail: %v", err)
	}
	if len(inv.Workloads) != 1 {
		t.Fatal("cost findings should still be possible without nodes")
	}
	if len(warnings) == 0 {
		t.Fatal("degrading silently is worse than failing; expected a warning")
	}
}

func TestTerminalPodsAreIgnored(t *testing.T) {
	p := pod("prod", "done", "n1", nil, ctr("x", "1Gi", "1Gi"))
	p.Status.Phase = corev1.PodSucceeded
	f := &fakeReader{nodes: []corev1.Node{node("n1", 8, nil)}, pods: []corev1.Pod{p}}
	inv, _, err := Collect(context.Background(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Nodes[0].Tenants) != 0 {
		t.Fatalf("a Succeeded pod holds no memory and must not count as a tenant: %v", inv.Nodes[0].Tenants)
	}
}

func TestUsageTakesTheWorstPodNotTheMean(t *testing.T) {
	stsRef := ctrlRef("StatefulSet", "db")
	usage := func(name string, mem string) metricsapi.PodMetrics {
		return metricsapi.PodMetrics{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: name},
			Containers: []metricsapi.ContainerMetrics{{Name: "db", Usage: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse(mem),
				corev1.ResourceCPU:    resource.MustParse("100m"),
			}}},
		}
	}
	f := &fakeReader{
		metricsUp: true,
		nodes:     []corev1.Node{node("n1", 16, nil)},
		pods: []corev1.Pod{
			pod("prod", "db-0", "n1", &stsRef, ctr("db", "1Gi", "2Gi")),
			pod("prod", "db-1", "n1", &stsRef, ctr("db", "1Gi", "2Gi")),
		},
		stss: []appsv1.StatefulSet{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "db"},
			Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{ctr("db", "1Gi", "2Gi")}},
			}},
		}},
		metrics: []metricsapi.PodMetrics{usage("db-0", "200Mi"), usage("db-1", "900Mi")},
	}
	inv, _, err := Collect(context.Background(), f, Options{WithMetrics: true})
	if err != nil {
		t.Fatal(err)
	}
	if !inv.MetricsAvailable {
		t.Fatalf("metrics should be available: %s", inv.MetricsNote)
	}
	ref := model.Ref{Kind: model.KindStatefulSet, Namespace: "prod", Name: "db"}
	if got, want := inv.Usage[ref].MemBytes, int64(900<<20); got != want {
		t.Fatalf("usage=%d, want the busiest pod's %d", got, want)
	}
}

func TestMetricsUnavailableIsRecordedNotFatal(t *testing.T) {
	f := &fakeReader{metricsUp: false}
	inv, _, err := Collect(context.Background(), f, Options{WithMetrics: true})
	if err != nil {
		t.Fatal(err)
	}
	if inv.MetricsAvailable {
		t.Fatal("metrics should be reported unavailable")
	}
	if inv.MetricsNote == "" {
		t.Fatal("capsize must say why usage data is missing, not just omit it")
	}
}
