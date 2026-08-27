package detect

import (
	"fmt"

	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// Contradictions are the reason capsize exists.
//
// A cost tool looks at a workload requesting 1Gi and using 200Mi and says:
// shrink the request, save 800Mi per replica. It is right about the money.
// What it cannot see is that the request was the only number holding that
// workload back. With no memory limit set, the request is not a reservation
// so much as a rough indication of intent; the container's actual ceiling is
// the node. Halve the request and you have not made the workload smaller, you
// have made it denser - more replicas per node, each still able to grow to the
// full node, now with more neighbors to take down when one of them does.
//
// So capsize prices every request reduction in both currencies and refuses to
// recommend one that raises blast radius without naming the guard that has to
// land first.
func contradictions(inv *model.Inventory, scores map[model.Ref]risk.Score, findings []Finding) []Finding {
	var out []Finding

	for _, f := range findings {
		rec := f.Recommendation
		if rec == nil || !rec.IncreasesBlastRadius || f.Ref == nil {
			continue
		}
		s := scores[*f.Ref]
		ref := *f.Ref

		multiple := 0.0
		if rec.RiskBefore > 0 {
			multiple = rec.RiskAfter / rec.RiskBefore
		}

		var why string
		if s.CeilingSource == risk.FromNode {
			why = fmt.Sprintf(
				"with no memory limit set, the request is the only number holding this workload below "+
					"%s of allocatable memory on %s, which it shares with %d other workload(s)",
				units.Bytes(s.Ceiling), s.Node, s.Neighbors)
		} else {
			why = fmt.Sprintf(
				"the memory limit stays at %s while the request drops to %s, widening the gap between "+
					"what is reserved and what may be consumed on %s alongside %d other workload(s)",
				units.Bytes(s.Ceiling), units.Bytes(rec.Proposed), s.Node, s.Neighbors)
		}

		saving := ""
		if rec.SavedBytes > 0 {
			saving = fmt.Sprintf("releases %s per pod", units.Bytes(rec.SavedBytes))
		} else {
			saving = "changes the reservation"
		}

		out = append(out, Finding{
			Rule:      RuleContradiction,
			Severity:  SeverityCritical,
			Title:     "cost recommendation would increase blast radius",
			Namespace: ref.Namespace,
			Ref:       &ref,
			Risk:      s.Risk,
			Detail: fmt.Sprintf(
				"cutting the %s from %s to %s %s but raises blast radius %s (%s -> %s): %s. Do this first: %s.",
				rec.Field, units.Bytes(rec.Current), units.Bytes(rec.Proposed), saving,
				units.Ratio(multiple), units.Score(rec.RiskBefore), units.Score(rec.RiskAfter),
				why, rec.Guard),
			Recommendation: rec,
		})
	}
	return out
}
