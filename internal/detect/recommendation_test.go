package detect

import (
	"strings"
	"testing"

	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/units"
)

// A recommendation is advice someone pastes into a manifest. Against a
// cluster of idle containers the arithmetic happily produced "512Mi -> 1Mi"
// and a contradiction reading "raises blast radius 512x (12.2 -> 6251)". Both
// numbers were correct and both were useless: nobody runs a 1Mi request, and
// the absurd multiple made a true finding look broken.
//
// capsize now picks between three honest answers depending on how far below
// the request the sample sits.
func TestRecommendationBands(t *testing.T) {
	const (
		ki = int64(1) << 10
		// A 16Gi node with 7 neighbors, so an unbounded workload has a real
		// blast radius and shrinking its request genuinely raises it.
		nodeGi    = 16
		neighbors = 7
	)

	cases := []struct {
		name  string
		req   int64 // declared memory request
		usage int64 // observed memory, busiest pod

		wantSeverity Severity
		wantRec      bool
		wantProposed int64 // only checked when wantRec
		// wantText are substrings the detail must contain; wantNoText are
		// substrings it must not.
		wantText   []string
		wantNoText []string
		// wantContradiction is whether CAP301 should be raised at all.
		wantContradiction bool
	}{
		{
			name:  "usage well above the floor: a real number",
			req:   512 * units.Mi,
			usage: 40 * units.Mi, // 12.8x over, 1.25x of usage is 50Mi

			wantSeverity: SeverityWarn, // escalated by the contradiction
			wantRec:      true,
			wantProposed: 50 * units.Mi,
			wantText:     []string{"512Mi", "40Mi", "13x over-provisioned", "a request of 50Mi", "1.2x headroom"},
			wantNoText:   []string{"min-request", "may be idle"},

			wantContradiction: true,
		},
		{
			name:  "usage just under the floor: recommend the floor and say so",
			req:   256 * units.Mi,
			usage: 8 * units.Mi, // 32x over, 1.25x of usage is 10Mi -> floored

			wantSeverity: SeverityWarn,
			wantRec:      true,
			wantProposed: 32 * units.Mi, // the --min-request floor, not 10Mi
			wantText: []string{
				"256Mi", "8Mi", "32x over-provisioned",
				"below anything worth writing in a manifest",
				"--min-request floor of 32Mi",
			},
			wantNoText: []string{"a request of 10Mi", "may be idle"},

			wantContradiction: true,
		},
		{
			name:  "usage far under the floor: not a sizing basis at all",
			req:   512 * units.Mi,
			usage: 200 * ki, // 2621x over: the workload was idle when sampled

			wantSeverity: SeverityInfo, // never escalates: there is no advice to escalate
			wantRec:      false,
			wantText: []string{
				"512Mi", "200Ki", ">1000x",
				"more likely an idle or scaled-down workload than a sizing error",
				"confirm it does work under load before resizing",
				"No request recommendation is offered from this sample.",
			},
			wantNoText: []string{"a request of", "headroom", "1Mi"},

			// A contradiction computed from a recommendation capsize declined
			// to make is not a finding.
			wantContradiction: false,
		},
		{
			name:  "request already at the floor: a real gap, nothing to prescribe",
			req:   32 * units.Mi,
			usage: 4 * units.Mi, // 8x over, but 5Mi floors up to the request itself

			wantSeverity: SeverityInfo,
			wantRec:      false,
			wantText:     []string{"32Mi", "4Mi", "8.0x over-provisioned", "already at or below the --min-request floor"},
			wantNoText:   []string{"a request of", "may be idle"},

			wantContradiction: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Unbounded on purpose: this is the shape where shrinking a
			// request raises blast radius, so the contradiction path is live.
			w := &model.Workload{Ref: ref("api"), Replicas: 2, Containers: []model.Container{
				container("app", memReq(tc.req)),
			}}
			inv, scores := scene(w, nodeGi, neighbors, false)
			inv.MetricsAvailable = true
			inv.Usage[w.Ref] = model.Usage{MemBytes: tc.usage, Samples: 2}

			fs := Run(inv, scores, Options{
				Divergence: 2, Headroom: 1.25,
				MinRequest: 32 * units.Mi, IdleRatio: 50,
			})

			f := has(fs, RuleMemOverProvision)
			if f == nil {
				t.Fatal("the over-provisioning gap must still be flagged; only the advice changes")
			}
			if f.Severity != tc.wantSeverity {
				t.Errorf("severity = %s, want %s", f.Severity, tc.wantSeverity)
			}

			switch {
			case tc.wantRec && f.Recommendation == nil:
				t.Fatal("expected a recommendation")
			case !tc.wantRec && f.Recommendation != nil:
				t.Fatalf("expected no recommendation, got %s -> %s",
					units.Bytes(f.Recommendation.Current), units.Bytes(f.Recommendation.Proposed))
			}
			if tc.wantRec && f.Recommendation.Proposed != tc.wantProposed {
				t.Errorf("proposed = %s, want %s",
					units.Bytes(f.Recommendation.Proposed), units.Bytes(tc.wantProposed))
			}

			for _, want := range tc.wantText {
				if !strings.Contains(f.Detail, want) {
					t.Errorf("detail is missing %q:\n  %s", want, f.Detail)
				}
			}
			for _, unwanted := range tc.wantNoText {
				if strings.Contains(f.Detail, unwanted) {
					t.Errorf("detail should not contain %q:\n  %s", unwanted, f.Detail)
				}
			}

			c := has(fs, RuleContradiction)
			if tc.wantContradiction && c == nil {
				t.Error("shrinking an unbounded request should raise a contradiction")
			}
			if !tc.wantContradiction && c != nil {
				t.Errorf("no recommendation was made, so there is nothing to contradict: %s", c.Detail)
			}
		})
	}
}

// The floor is a promise about every recommendation capsize emits, not just
// the memory rule that motivated it.
func TestNoRecommendationEverFallsBelowTheFloor(t *testing.T) {
	const floor = 32 * units.Mi

	for _, usage := range []int64{1, 1024, 200 * 1024, units.Mi, 7 * units.Mi, 31 * units.Mi, 40 * units.Mi} {
		w := &model.Workload{Ref: ref("api"), Replicas: 1, Containers: []model.Container{
			container("app", memReq(2*units.Gi)),
		}}
		inv, scores := scene(w, 16, 5, false)
		inv.MetricsAvailable = true
		inv.Usage[w.Ref] = model.Usage{MemBytes: usage, Samples: 1}

		for _, f := range Run(inv, scores, Options{
			Divergence: 2, Headroom: 1.25, MinRequest: floor, IdleRatio: 50,
		}) {
			if f.Recommendation == nil || f.Recommendation.Field != "memory request" {
				continue
			}
			if f.Recommendation.Proposed < floor {
				t.Errorf("usage %s produced a recommendation of %s, below the floor of %s",
					units.Bytes(usage), units.Bytes(f.Recommendation.Proposed), units.Bytes(floor))
			}
		}
	}
}

// The idle argument is about the sample, not about memory: an instant reading
// of a workload doing nothing sizes CPU no better.
func TestIdleSampleOffersNoCPUNumberEither(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Replicas: 1, Containers: []model.Container{
		container("app", memReq(128*units.Mi), cpuReq(1000)),
	}}
	inv, scores := scene(w, 16, 5, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 100 * units.Mi, CPUMillis: 1, Samples: 1}

	fs := Run(inv, scores, Options{Divergence: 2, Headroom: 1.25, MinRequest: 32 * units.Mi, IdleRatio: 50})
	f := has(fs, RuleCPUOverProvision)
	if f == nil {
		t.Fatal("a 1000m request against 1m of usage must still be flagged")
	}
	if f.Recommendation != nil {
		t.Errorf("an idle sample is not a CPU sizing basis, got a proposal of %dm", f.Recommendation.Proposed)
	}
	if !strings.Contains(f.Detail, "No request recommendation is offered from this sample.") {
		t.Errorf("detail should decline to size from an idle sample:\n  %s", f.Detail)
	}
}

// The defaults are part of the contract: someone running capsize with no
// flags must get credible advice.
func TestDefaultsApplyTheFloorAndTheIdleThreshold(t *testing.T) {
	w := &model.Workload{Ref: ref("api"), Replicas: 1, Containers: []model.Container{
		container("app", memReq(256*units.Mi)),
	}}
	inv, scores := scene(w, 16, 5, false)
	inv.MetricsAvailable = true
	inv.Usage[w.Ref] = model.Usage{MemBytes: 8 * units.Mi, Samples: 1}

	f := has(Run(inv, scores, Options{}), RuleMemOverProvision)
	if f == nil || f.Recommendation == nil {
		t.Fatal("expected a floored recommendation")
	}
	if f.Recommendation.Proposed != 32*units.Mi {
		t.Errorf("proposed = %s, want the default 32Mi floor", units.Bytes(f.Recommendation.Proposed))
	}
}
