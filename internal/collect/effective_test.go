package collect

import (
	"context"
	"math"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// Kubernetes' core API defaults a container's requests to its limits when the
// limits are set and the requests are omitted. It is not a LimitRange, it
// happens on every cluster, and it is invisible in the PodTemplateSpec -
// which is what capsize reads.
//
// Reading the template verbatim made a Guaranteed pod look like it had no
// requests at all, so it was scored against --request-floor and ranked second
// riskiest in the fixture cluster at 237.8 when its true score is 2.32.
//
// This table covers all four request/limit combinations end to end: pod spec
// in, effective requests, QoS class and blast-radius score out. The
// "limits only" row is the regression guard.
func TestEffectiveResourcesAcrossAllFourCombinations(t *testing.T) {
	const (
		nodeGi    = 16
		neighbors = 4
		floor     = 10 * units.Mi
	)

	cases := []struct {
		name string

		requests map[corev1.ResourceName]string
		limits   map[corev1.ResourceName]string

		wantMemRequest int64
		wantCPURequest int64
		wantDefaulted  bool
		wantQoS        model.QoSClass

		// wantCeiling is the memory the workload could reach on the node.
		wantCeiling int64
		// wantAssumed is whether the score fell back to --request-floor.
		wantAssumed bool
		wantRatio   float64
	}{
		{
			name:     "both set",
			requests: map[corev1.ResourceName]string{"memory": "128Mi", "cpu": "100m"},
			limits:   map[corev1.ResourceName]string{"memory": "256Mi", "cpu": "500m"},

			wantMemRequest: 128 * units.Mi,
			wantCPURequest: 100,
			wantDefaulted:  false,
			wantQoS:        model.QoSBurstable,
			wantCeiling:    256 * units.Mi,
			wantRatio:      2,
		},
		{
			name:     "requests only",
			requests: map[corev1.ResourceName]string{"memory": "256Mi", "cpu": "200m"},

			wantMemRequest: 256 * units.Mi,
			wantCPURequest: 200,
			wantDefaulted:  false,
			wantQoS:        model.QoSBurstable,
			// Nothing bounds it, so the ceiling is the whole node.
			wantCeiling: nodeGi * units.Gi,
			wantRatio:   float64(nodeGi*units.Gi) / float64(256*units.Mi),
		},
		{
			name:   "limits only",
			limits: map[corev1.ResourceName]string{"memory": "1Gi", "cpu": "1"},

			// The regression: these must be the limits, not zero.
			wantMemRequest: units.Gi,
			wantCPURequest: 1000,
			wantDefaulted:  true,
			// requests == limits on both resources: the least evictable class,
			// not the most.
			wantQoS:     model.QoSGuaranteed,
			wantCeiling: units.Gi,
			wantAssumed: false,
			wantRatio:   1,
		},
		{
			name: "neither",

			wantMemRequest: 0,
			wantCPURequest: 0,
			wantDefaulted:  false,
			wantQoS:        model.QoSBestEffort,
			wantCeiling:    nodeGi * units.Gi,
			// Only here, with nothing declared at all, does the floor apply.
			wantAssumed: true,
			wantRatio:   float64(nodeGi*units.Gi) / float64(floor),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := collectOne(t, resourcesFrom(tc.requests, tc.limits), nodeGi, neighbors)
			w := inv.Workloads[0]

			if len(w.Containers) != 1 {
				t.Fatalf("want 1 container, got %d", len(w.Containers))
			}
			c := w.Containers[0]

			if got := w.MemRequest(); got != tc.wantMemRequest {
				t.Errorf("effective memory request = %s, want %s",
					units.Bytes(got), units.Bytes(tc.wantMemRequest))
			}
			if got := w.CPURequest(); got != tc.wantCPURequest {
				t.Errorf("effective cpu request = %s, want %s",
					units.CPU(got), units.CPU(tc.wantCPURequest))
			}
			if c.MemRequestDefaulted != tc.wantDefaulted {
				t.Errorf("MemRequestDefaulted = %v, want %v", c.MemRequestDefaulted, tc.wantDefaulted)
			}
			if got := w.QoS(); got != tc.wantQoS {
				t.Errorf("QoS = %s, want %s", got, tc.wantQoS)
			}

			s := risk.Of(w, inv, risk.Options{RequestFloor: floor})
			if !s.Scored {
				t.Fatalf("not scored: %s", s.Reason)
			}
			if s.RequestAssumed != tc.wantAssumed {
				t.Errorf("RequestAssumed = %v, want %v (the floor applies only when "+
					"neither requests nor limits are set)", s.RequestAssumed, tc.wantAssumed)
			}
			if s.Ceiling != tc.wantCeiling {
				t.Errorf("ceiling = %s, want %s", units.Bytes(s.Ceiling), units.Bytes(tc.wantCeiling))
			}
			if math.Abs(s.Ratio-tc.wantRatio) > 1e-6 {
				t.Errorf("ratio = %v, want %v", s.Ratio, tc.wantRatio)
			}

			wantRisk := tc.wantRatio * math.Log2(1+neighbors)
			if math.Abs(s.Risk-wantRisk) > 1e-6 {
				t.Errorf("risk = %v, want %v", s.Risk, wantRisk)
			}
		})
	}
}

// TestLimitsOnlyMatchesTheFixtureOracle pins the exact number the fixture
// cluster produced, so a future refactor cannot quietly reintroduce the bug.
func TestLimitsOnlyMatchesTheFixtureOracle(t *testing.T) {
	inv := collectOne(t, resourcesFrom(
		nil,
		map[corev1.ResourceName]string{"memory": "1Gi", "cpu": "1"},
	), 16, 4)

	s := risk.Of(inv.Workloads[0], inv, risk.Options{RequestFloor: 10 * units.Mi})

	const want = 2.321928 // 1.0 * log2(5) * 1.0
	if math.Abs(s.Risk-want) > 1e-5 {
		t.Fatalf("risk = %v, want %v (was 237.77 before effective resolution)", s.Risk, want)
	}
	if s.Request != units.Gi {
		t.Errorf("request = %s, want 1Gi", units.Bytes(s.Request))
	}
	if s.RequestAssumed {
		t.Error("a limits-only workload has a real request; the floor must not apply")
	}
}

// --- helpers --------------------------------------------------------------

func resourcesFrom(requests, limits map[corev1.ResourceName]string) corev1.ResourceRequirements {
	out := corev1.ResourceRequirements{}
	if len(requests) > 0 {
		out.Requests = corev1.ResourceList{}
		for k, v := range requests {
			out.Requests[k] = resource.MustParse(v)
		}
	}
	if len(limits) > 0 {
		out.Limits = corev1.ResourceList{}
		for k, v := range limits {
			out.Limits[k] = resource.MustParse(v)
		}
	}
	return out
}

// collectOne builds a one-deployment cluster on a node with n neighbors and
// runs it through the real collection path, so the test exercises the code
// that reads the PodTemplateSpec rather than a hand-built model.
func collectOne(t *testing.T, res corev1.ResourceRequirements, nodeGi int64, neighbors int) *model.Inventory {
	t.Helper()

	rsRef := ctrlRef("ReplicaSet", "subject-1")
	pods := []corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "sandbox", Name: "subject-1-abc",
			OwnerReferences: []metav1.OwnerReference{rsRef},
		},
		Spec: corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{
			{Name: "app", Resources: res},
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}}
	for i := 0; i < neighbors; i++ {
		name := string(rune('a' + i))
		owner := ctrlRef("DaemonSet", name)
		pods = append(pods, pod("kube-system", name+"-pod", "n1", &owner, ctr("x", "8Mi", "8Mi")))
	}

	f := &fakeReader{
		nodes: []corev1.Node{node("n1", nodeGi, nil)},
		rss: []appsv1.ReplicaSet{{ObjectMeta: metav1.ObjectMeta{
			Namespace: "sandbox", Name: "subject-1",
			OwnerReferences: []metav1.OwnerReference{ctrlRef("Deployment", "subject")},
		}}},
		deploys: []appsv1.Deployment{{
			ObjectMeta: metav1.ObjectMeta{Namespace: "sandbox", Name: "subject"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Resources: res}}},
			}},
		}},
		pods: pods,
	}

	inv, _, err := Collect(context.Background(), f, Options{Namespace: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Workloads) != 1 {
		t.Fatalf("want 1 workload in scope, got %d", len(inv.Workloads))
	}
	return inv
}
