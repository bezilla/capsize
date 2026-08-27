// Package risk turns a workload's declared shape and its node's reality into a
// single blast-radius number.
//
// The formula, verbatim:
//
//	ceiling     = min(node_allocatable_mem, container_mem_limit)
//	ratio       = ceiling / workload_mem_request
//	neighbours  = distinct OTHER workloads schedulable on that node
//	spot_factor = 1.5 on interruptible capacity, else 1.0
//	risk        = ratio * log2(1 + neighbours) * spot_factor
//
// Two consequences are intended rather than accidental, and both are the
// thesis of the tool expressed arithmetically:
//
//   - A workload with no memory limit has no ceiling of its own, so the
//     ceiling falls back to the whole node. An unbounded workload therefore
//     scores dramatically higher than a bounded one, because it genuinely is
//     dramatically more dangerous: nothing stops it walking the node to zero
//     and taking every neighbour with it.
//   - A workload alone on its node scores zero, because log2(1+0) is zero.
//     It has nothing to take down but itself. That is not a bug and it is not
//     a clean bill of health; it is the honest answer to the question asked.
package risk

import (
	"math"

	"github.com/bezilla/capsize/internal/model"
)

// SpotFactor is the multiplier applied on interruptible capacity: the node can
// vanish on somebody else's schedule, so the same overcommit hurts more.
const SpotFactor = 1.5

// Options tunes the parts of the score that are judgement calls.
type Options struct {
	// RequestFloor is the memory request assumed for a workload that declares
	// none. Without it the ratio is a division by zero, and an infinity sorts
	// to the top of the table but tells you nothing about which infinity is
	// worse. The floor keeps the score finite, ordered and comparable; the
	// finding always records that the number was assumed.
	RequestFloor int64
}

// CeilingSource says which term of the min() won, which is the single most
// useful thing to know about a high score.
type CeilingSource string

const (
	// FromLimit means the container's own memory limit bounds it.
	FromLimit CeilingSource = "container limit"
	// FromNode means nothing bounds it but the node itself.
	FromNode CeilingSource = "node allocatable"
)

// Score is one workload's blast radius, with every input kept so the number
// can be argued with rather than merely believed.
type Score struct {
	Ref model.Ref `json:"ref"`

	Risk  float64 `json:"risk"`
	Ratio float64 `json:"ratio"`

	Ceiling        int64         `json:"ceilingBytes"`
	CeilingSource  CeilingSource `json:"ceilingSource"`
	Request        int64         `json:"requestBytes"`
	RequestAssumed bool          `json:"requestAssumed"`

	Node       string  `json:"node"`
	Neighbours int     `json:"neighbours"`
	Spot       bool    `json:"spot"`
	SpotFactor float64 `json:"spotFactor"`

	// Hypothetical marks a workload with no running pods, scored against the
	// largest node it could land on. It is a forecast, not an observation.
	Hypothetical bool `json:"hypothetical,omitempty"`

	// Scored is false when there was no node to score against at all.
	Scored bool   `json:"scored"`
	Reason string `json:"reason,omitempty"`
}

// SoleTenant reports the zero-risk-but-not-safe case: scored, but with nothing
// on the node to endanger.
func (s Score) SoleTenant() bool { return s.Scored && s.Neighbours == 0 }

// Compute is the formula itself, and the only place it is written down.
// Callers pass the four inputs; both live scoring and what-if projection go
// through here so a recommendation can never be evaluated against different
// arithmetic than the finding that prompted it.
func Compute(ceiling, request int64, neighbours int, spot bool) (riskScore, ratio float64) {
	if request <= 0 || ceiling <= 0 {
		return 0, 0
	}
	ratio = float64(ceiling) / float64(request)
	factor := 1.0
	if spot {
		factor = SpotFactor
	}
	return ratio * math.Log2(1+float64(neighbours)) * factor, ratio
}

// Of computes the blast radius of w against every node it currently
// occupies and returns the worst of them. Blast radius is a worst-case
// question: the mean across nodes would hide the one node where this workload
// sits next to thirty neighbours.
func Of(w *model.Workload, inv *model.Inventory, o Options) Score {
	s := Score{Ref: w.Ref, SpotFactor: 1.0}

	request := w.MemRequest()
	if request <= 0 {
		request = o.RequestFloor
		s.RequestAssumed = true
	}
	s.Request = request

	limit, bounded := w.MemLimit()

	nodes, hypothetical := candidateNodes(w, inv)
	if len(nodes) == 0 {
		s.Reason = "no node available to score against"
		if len(inv.Nodes) == 0 {
			s.Reason = "node list unavailable"
		}
		return s
	}
	s.Hypothetical = hypothetical

	for _, n := range nodes {
		if n.AllocatableMem <= 0 {
			continue
		}
		ceiling, source := n.AllocatableMem, FromNode
		if bounded && limit > 0 && limit < n.AllocatableMem {
			ceiling, source = limit, FromLimit
		}
		neighbours := n.Neighbours(w.Ref)
		r, ratio := Compute(ceiling, request, neighbours, n.Spot)

		if !s.Scored || r > s.Risk {
			s.Scored = true
			s.Risk, s.Ratio = r, ratio
			s.Ceiling, s.CeilingSource = ceiling, source
			s.Node, s.Neighbours = n.Name, neighbours
			s.Spot = n.Spot
			s.SpotFactor = 1.0
			if n.Spot {
				s.SpotFactor = SpotFactor
			}
		}
	}
	if !s.Scored {
		s.Reason = "no node reported allocatable memory"
	}
	return s
}

// Project answers "what would this score become if the request changed to
// request and the ceiling to ceiling?", holding node, neighbours and capacity
// type fixed. It is how capsize prices a cost recommendation in blast-radius
// terms before suggesting it.
func (s Score) Project(request, ceiling int64) (riskScore, ratio float64) {
	return Compute(ceiling, request, s.Neighbours, s.Spot)
}

// candidateNodes returns the nodes to score against: the ones the workload's
// pods actually occupy, or - for a workload with no running pods - the single
// largest node in the cluster, since that is the roomiest ceiling it could
// inherit and therefore its worst case.
func candidateNodes(w *model.Workload, inv *model.Inventory) (nodes []*model.Node, hypothetical bool) {
	for _, name := range w.Nodes {
		if n := inv.NodeByName(name); n != nil {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) > 0 {
		return nodes, false
	}
	var largest *model.Node
	for _, n := range inv.Nodes {
		if largest == nil || n.AllocatableMem > largest.AllocatableMem {
			largest = n
		}
	}
	if largest == nil {
		return nil, false
	}
	return []*model.Node{largest}, true
}

// All scores every workload in the inventory.
func All(inv *model.Inventory, o Options) map[model.Ref]Score {
	out := make(map[model.Ref]Score, len(inv.Workloads))
	for _, w := range inv.Workloads {
		out[w.Ref] = Of(w, inv, o)
	}
	return out
}
