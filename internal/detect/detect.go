package detect

import (
	"fmt"

	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/risk"
	"github.com/bezilla/capsize/internal/units"
)

// Options tunes the thresholds that are judgement calls rather than facts.
type Options struct {
	// Divergence is the factor by which a request must exceed observed usage
	// before capsize calls it over-provisioned.
	Divergence float64
	// Headroom multiplies observed usage when proposing a new request.
	Headroom float64
	// RiskIncreaseTolerance is the fractional rise in blast radius below which
	// a recommendation is not worth arguing about. Default 0.05.
	RiskIncreaseTolerance float64
}

func (o Options) withDefaults() Options {
	if o.Divergence <= 1 {
		o.Divergence = 2
	}
	if o.Headroom < 1 {
		o.Headroom = 1.25
	}
	if o.RiskIncreaseTolerance <= 0 {
		o.RiskIncreaseTolerance = 0.05
	}
	return o
}

// Run produces every finding for an inventory. scores must be the scores for
// the same inventory; contradiction detection is meaningless otherwise.
func Run(inv *model.Inventory, scores map[model.Ref]risk.Score, o Options) []Finding {
	o = o.withDefaults()
	var out []Finding

	for _, w := range inv.Workloads {
		s := scores[w.Ref]
		out = append(out, shapeFindings(w, s)...)
		if inv.MetricsAvailable {
			if u, ok := inv.Usage[w.Ref]; ok {
				out = append(out, usageFindings(w, s, u, o)...)
			}
		}
	}

	out = append(out, namespaceFindings(inv)...)

	// Contradictions are derived from the findings above rather than
	// rediscovered, so a contradiction can never disagree with the
	// recommendation that produced it.
	out = append(out, contradictions(inv, scores, out)...)

	Sort(out)
	return out
}

// --- static shape --------------------------------------------------------

func shapeFindings(w *model.Workload, s risk.Score) []Finding {
	var out []Finding
	base := func(rule string, sev Severity, title, container, detail string) Finding {
		ref := w.Ref
		return Finding{
			Rule: rule, Severity: sev, Title: title,
			Namespace: w.Ref.Namespace, Ref: &ref, Container: container,
			Detail: detail, Risk: s.Risk,
		}
	}

	qos := w.QoS()

	for _, c := range w.Containers {
		// These are EFFECTIVE resources: a container that declares only limits
		// has requests, because Kubernetes defaulted them to the limits. The
		// scheduler does reserve for it, so CAP101 must not fire.
		anyRequest := c.HasCPURequest || c.HasMemRequest
		anyLimit := c.HasCPULimit || c.HasMemLimit

		if !anyRequest {
			out = append(out, base(RuleNoRequests, SeverityWarn,
				"no resource requests", c.Name,
				fmt.Sprintf("declares neither requests nor limits, so the scheduler treats it as free "+
					"and packs it onto any node with room; the pod is %s and is evicted first", qos)))
		}
		if !anyLimit {
			sev := SeverityWarn
			detail := "declares no CPU or memory limit, so nothing but the node's own capacity bounds it"
			if s.Scored && s.CeilingSource == risk.FromNode && s.Neighbours > 0 {
				sev = SeverityCritical
				detail = fmt.Sprintf(
					"declares no CPU or memory limit, so its memory ceiling is the whole of %s (%s) "+
						"and it shares that node with %d other workload(s)",
					s.Node, units.Bytes(s.Ceiling), s.Neighbours)
			}
			out = append(out, base(RuleNoLimits, sev, "no resource limits", c.Name, detail))
		}

		// The asymmetric cases. A limit without a request is the more
		// dangerous direction: the scheduler reserves nothing, but the kernel
		// still lets the container grow to the limit.
		// Manifest hygiene, not a hazard. Kubernetes defaults the request to
		// the limit, so the reservation is real and correct - it is simply
		// nowhere in the manifest, which means it changes silently the day
		// someone edits the limit.
		if c.HasMemLimit && c.MemRequestDefaulted {
			out = append(out, base(RuleMemLimitNoReq, SeverityInfo,
				"memory request left implicit", c.Name,
				fmt.Sprintf("limits memory to %s and declares no memory request, so Kubernetes defaults "+
					"the request to the limit: the scheduler reserves %s and this pod is %s. The outcome "+
					"is right but implicit - spell out requests.memory: %s so the reservation cannot "+
					"move silently when someone edits the limit",
					units.Bytes(c.MemLimit), units.Bytes(c.MemRequest), qos, units.Bytes(c.MemLimit))))
		}
		if c.HasMemRequest && !c.MemRequestDefaulted && !c.HasMemLimit && anyLimit {
			out = append(out, base(RuleMemReqNoLimit, SeverityWarn,
				"memory request without a limit", c.Name,
				fmt.Sprintf("requests %s of memory with no memory limit; the request is a floor, not a "+
					"ceiling, and nothing stops this container consuming the node",
					units.Bytes(c.MemRequest))))
		}
		if c.HasCPULimit && c.CPURequestDefaulted {
			out = append(out, base(RuleCPULimitNoReq, SeverityInfo,
				"CPU request left implicit", c.Name,
				fmt.Sprintf("limits CPU to %s and declares no CPU request, so Kubernetes defaults the "+
					"request to the limit: the scheduler reserves %s and this pod is %s. Spell out "+
					"requests.cpu: %s so the reservation cannot move silently when someone edits the limit",
					units.CPU(c.CPULimit), units.CPU(c.CPURequest), qos, units.CPU(c.CPULimit))))
		}
		if c.HasCPURequest && !c.CPURequestDefaulted && !c.HasCPULimit && anyLimit {
			out = append(out, base(RuleCPUReqNoLimit, SeverityInfo,
				"CPU request without a limit", c.Name,
				fmt.Sprintf("requests %s of CPU with no CPU limit; this is often deliberate, since CPU "+
					"is compressible and throttling hurts latency", units.CPU(c.CPURequest))))
		}
	}
	return out
}

// --- observed usage ------------------------------------------------------

func usageFindings(w *model.Workload, s risk.Score, u model.Usage, o Options) []Finding {
	var out []Finding
	ref := w.Ref
	memReq := w.MemRequest()

	if memReq > 0 && u.MemBytes > 0 {
		switch {
		case float64(memReq) > float64(u.MemBytes)*o.Divergence:
			proposed := units.RoundUpMi(int64(float64(u.MemBytes) * o.Headroom))
			if proposed < units.Mi {
				proposed = units.Mi
			}
			rec := priceRecommendation(s, "memory request", memReq, proposed, o)

			sev := SeverityInfo
			detail := fmt.Sprintf(
				"requests %s of memory but its busiest pod uses %s (%s over-provisioned across %d replica(s)); "+
					"a request of %s would hold %s headroom",
				units.Bytes(memReq), units.Bytes(u.MemBytes),
				units.Ratio(float64(memReq)/float64(u.MemBytes)), w.Replicas,
				units.Bytes(proposed), units.Ratio(o.Headroom))
			if rec.IncreasesBlastRadius {
				sev = SeverityWarn
			}
			f := Finding{
				Rule: RuleMemOverProvision, Severity: sev,
				Title:     "memory request far above observed usage",
				Namespace: w.Ref.Namespace, Ref: &ref, Detail: detail, Risk: s.Risk,
				Recommendation: &rec,
			}
			out = append(out, f)

		case u.MemBytes > memReq:
			out = append(out, Finding{
				Rule: RuleMemUnderProvison, Severity: SeverityWarn,
				Title:     "observed memory above the request",
				Namespace: w.Ref.Namespace, Ref: &ref, Risk: s.Risk,
				Detail: fmt.Sprintf(
					"requests %s of memory but its busiest pod already uses %s; the excess is unreserved, "+
						"so this pod is evicted before anything that reserved what it uses",
					units.Bytes(memReq), units.Bytes(u.MemBytes)),
			})
		}
	}

	if cpuReq := w.CPURequest(); cpuReq > 0 && u.CPUMillis > 0 &&
		float64(cpuReq) > float64(u.CPUMillis)*o.Divergence {
		proposed := units.RoundUpMilli(int64(float64(u.CPUMillis) * o.Headroom))
		out = append(out, Finding{
			Rule: RuleCPUOverProvision, Severity: SeverityInfo,
			Title:     "CPU request far above observed usage",
			Namespace: w.Ref.Namespace, Ref: &ref, Risk: s.Risk,
			Detail: fmt.Sprintf(
				"requests %s of CPU but its busiest pod uses %s (%s over-provisioned); %s would hold %s headroom",
				units.CPU(cpuReq), units.CPU(u.CPUMillis),
				units.Ratio(float64(cpuReq)/float64(u.CPUMillis)),
				units.CPU(proposed), units.Ratio(o.Headroom)),
			Recommendation: &Recommendation{
				Field: "cpu request", Current: cpuReq, Proposed: proposed,
				RiskBefore: s.Risk, RiskAfter: s.Risk,
			},
		})
	}

	return out
}

// priceRecommendation states what a proposed memory request does to blast
// radius, using the same arithmetic that produced the original score.
func priceRecommendation(s risk.Score, field string, current, proposed int64, o Options) Recommendation {
	rec := Recommendation{
		Field:      field,
		Current:    current,
		Proposed:   proposed,
		SavedBytes: current - proposed,
		RiskBefore: s.Risk,
		RiskAfter:  s.Risk,
	}
	if !s.Scored {
		return rec
	}
	after, _ := s.Project(proposed, s.Ceiling)
	rec.RiskAfter = after

	if s.Risk > 0 && after > s.Risk*(1+o.RiskIncreaseTolerance) {
		rec.IncreasesBlastRadius = true
		if s.CeilingSource == risk.FromNode {
			rec.Guard = fmt.Sprintf("set a memory limit (nothing currently bounds this below %s of node %s)",
				units.Bytes(s.Ceiling), s.Node)
		} else {
			rec.Guard = fmt.Sprintf("lower the memory limit alongside the request (currently %s, %s of the proposed request)",
				units.Bytes(s.Ceiling), units.Ratio(float64(s.Ceiling)/float64(proposed)))
		}
	}
	return rec
}

// --- namespace guardrails ------------------------------------------------

func namespaceFindings(inv *model.Inventory) []Finding {
	var out []Finding
	for _, ns := range inv.Namespaces {
		if !ns.Ungoverned() {
			continue
		}
		sev := SeverityInfo
		if ns.Workloads > 0 {
			sev = SeverityWarn
		}
		out = append(out, Finding{
			Rule: RuleNoGuardrails, Severity: sev,
			Title:     "namespace has no LimitRange and no ResourceQuota",
			Namespace: ns.Name,
			Detail: fmt.Sprintf(
				"neither a LimitRange nor a ResourceQuota exists in %s, so a pod admitted here may "+
					"declare nothing and consume everything; %d workload(s) currently rely on that",
				ns.Name, ns.Workloads),
		})
	}
	return out
}
