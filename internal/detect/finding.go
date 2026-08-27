// Package detect turns an inventory and its blast-radius scores into findings.
//
// Findings come in two flavours that most tools keep in separate products:
// cost findings (this workload reserves more than it uses, or reserves
// nothing at all) and exposure findings (this workload can take its
// neighbours down). capsize keeps them in one list precisely so it can report
// the case where acting on the first makes the second worse.
package detect

import (
	"sort"

	"github.com/bezilla/capsize/internal/model"
)

// Severity orders findings for display and for --fail-on.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// rank orders severities; unknown strings sort below info.
func rank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarn:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether s is as severe as floor. Used by --fail-on.
func AtLeast(s, floor Severity) bool { return rank(s) >= rank(floor) }

// ParseSeverity accepts the --fail-on values, including "none".
func ParseSeverity(s string) (Severity, bool) {
	switch s {
	case "info":
		return SeverityInfo, true
	case "warn", "warning":
		return SeverityWarn, true
	case "critical", "crit":
		return SeverityCritical, true
	default:
		return "", false
	}
}

// Rule IDs are stable across releases so a team can suppress or track one.
const (
	RuleNoRequests       = "CAP101" // container declares no requests at all
	RuleNoLimits         = "CAP102" // container declares no limits at all
	RuleMemLimitNoReq    = "CAP103" // memory limit without a memory request
	RuleMemReqNoLimit    = "CAP104" // memory request without a memory limit
	RuleCPULimitNoReq    = "CAP105" // cpu limit without a cpu request
	RuleCPUReqNoLimit    = "CAP106" // cpu request without a cpu limit
	RuleMemOverProvision = "CAP107" // memory request far above observed usage
	RuleMemUnderProvison = "CAP108" // observed usage above the memory request
	RuleCPUOverProvision = "CAP109" // cpu request far above observed usage
	RuleNoGuardrails     = "CAP201" // namespace with neither LimitRange nor ResourceQuota
	RuleContradiction    = "CAP301" // the cost fix would increase blast radius
)

// Recommendation is a proposed change, priced in both currencies.
//
// Every recommendation capsize prints carries its blast-radius consequence,
// not just its cost saving. A recommendation whose RiskAfter exceeds its
// RiskBefore is one capsize will not make without also naming the guard that
// has to land first.
type Recommendation struct {
	Field    string `json:"field"`    // e.g. "memory request"
	Current  int64  `json:"current"`  // bytes or millicores, per Field
	Proposed int64  `json:"proposed"` // bytes or millicores, per Field

	// SavedBytes is the per-pod reservation released. Negative means the
	// recommendation costs capacity rather than saving it.
	SavedBytes int64 `json:"savedBytes,omitempty"`

	RiskBefore float64 `json:"riskBefore"`
	RiskAfter  float64 `json:"riskAfter"`

	// IncreasesBlastRadius is the flag this whole tool exists to raise.
	IncreasesBlastRadius bool `json:"increasesBlastRadius"`

	// Guard is the change that must land first for the recommendation to be
	// safe. Empty when the recommendation is safe on its own.
	Guard string `json:"guard,omitempty"`
}

// Finding is one thing capsize noticed.
type Finding struct {
	Rule      string   `json:"rule"`
	Severity  Severity `json:"severity"`
	Title     string   `json:"title"`
	Namespace string   `json:"namespace"`

	// Ref is the workload the finding is about; nil for namespace findings.
	Ref *model.Ref `json:"ref,omitempty"`

	// Container narrows a finding to one container of a multi-container pod.
	Container string `json:"container,omitempty"`

	// Detail is the sentence a human reads. It states the numbers, not just
	// the rule name.
	Detail string `json:"detail"`

	// Risk is the workload's blast-radius score, carried onto the finding so
	// a findings list can be triaged without cross-referencing the table.
	Risk float64 `json:"risk"`

	Recommendation *Recommendation `json:"recommendation,omitempty"`
}

// Sort orders findings the way a human triages them: worst severity first,
// then highest blast radius, then stably by identity.
func Sort(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if ra, rb := rank(a.Severity), rank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.Risk != b.Risk {
			return a.Risk > b.Risk
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		an, bn := "", ""
		if a.Ref != nil {
			an = a.Ref.Name
		}
		if b.Ref != nil {
			bn = b.Ref.Name
		}
		if an != bn {
			return an < bn
		}
		if a.Container != b.Container {
			return a.Container < b.Container
		}
		return a.Rule < b.Rule
	})
}

// Worst returns the highest severity present, and false if fs is empty.
func Worst(fs []Finding) (Severity, bool) {
	if len(fs) == 0 {
		return "", false
	}
	worst := fs[0].Severity
	for _, f := range fs[1:] {
		if rank(f.Severity) > rank(worst) {
			worst = f.Severity
		}
	}
	return worst, true
}
