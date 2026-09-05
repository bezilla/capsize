# Design notes

This document records the decisions behind capsize's blast-radius score and
what I considered and threw away. The README says what the tool does. This
says why it computes what it computes, so you can decide whether to trust the
number.

Where a choice rests on judgment rather than evidence, I say so. Where I
have no principled reason for a value, I say that too rather than
manufacturing one after the fact.

---

## The formula

```
ceiling     = min(node_allocatable_mem, container_mem_limit)
ratio       = ceiling / workload_mem_request
neighbors  = distinct other workloads schedulable on that node
spot_factor = 1.5 if the node is spot/preemptible else 1.0
risk        = ratio × log2(1 + neighbors) × spot_factor
```

---

## 1. The ceiling is a `min()`, not a special case

The ceiling is `min(node allocatable memory, container memory limit)`.

The alternative I considered was a special case: compute risk from node
allocatable, then discount it when a limit exists.

I rejected that because the `min()` gets there on its own. A limit collapses
the ceiling automatically, so the metric rewards bounding a workload as a
consequence of the arithmetic rather than as a rule bolted on beside it.
Fewer branches, same result.

The fallback behavior follows from the same expression. With no limit set
there is nothing to take the minimum against, so the ceiling is the whole
node, and an unbounded workload scores dramatically higher than an identical
bounded one. That is not a special case either; it is what the formula
already says.

## 2. `log2(1 + neighbors)`, not linear

The second tenant on a node matters far more than the twelfth. Linear scaling
would let crowded nodes dominate the ranking purely by count, and drown out a
genuinely unbounded workload sitting on a quiet node.

That argument justifies a **sublinear** term. It does not by itself pick
`log2` over any other sublinear shape — I do not have a principled reason for
the specific curve beyond it being the obvious one, and I would not defend it
against, say, a square root without new evidence.

One consequence is worth stating because it surprises people: a workload
alone on its node scores exactly zero, since `log2(1 + 0) = 0`. It has
nothing to take down but itself. `Score.SoleTenant()` exists so the output
can distinguish that case from a workload that is genuinely safe — a zero
here means "nothing to endanger", not "well configured".

## 3. `spot_factor = 1.5`

Preemption changes node packing, and changed packing is what turned a latent
bug into a production outage.

The multiplier is a judgment, not a measurement. I did not derive 1.5 from
anything, and I have no argument for it beyond the one above.

## 4. Observed usage takes the busiest pod, not the mean

metrics-server serves an instant, not a window. Sizing off the quietest
replica is how a rightsizing becomes a page.

## 5. `--request-floor` (default 10Mi)

A workload that declares no memory request has no denominator, and the ratio
is a division by zero. The floor substitutes a nominal request so the score
stays finite, sortable and JSON-encodable.

It is rendered as `~10Mi` in the table so a reader can see the number was
assumed rather than measured.

I have no principled basis for 10Mi specifically. The figure is arbitrary;
the flag exists so you can disagree with it. Note that because it is a
denominator, a smaller floor produces a larger score for every workload that
declares no request.

## 6. `--min-request` (32Mi) and `--idle-ratio` (50x) apply to advice, never to the score

Run capsize against a cluster of idle containers and the arithmetic works
perfectly. It recommended shrinking requests to **1Mi** and projected
contradictions of **512x**.

Both numbers were correct. Both were useless. Nobody runs a 1Mi memory
request, so the advice is not advice; and an absurd multiple attached to a
finding makes a *true* finding look broken.

Two rules now sit in front of the recommendation:

- **`--min-request`** — never recommend a memory request below this floor.
  When observed usage times headroom lands underneath it, capsize recommends
  the floor and says in the finding that it did.
- **`--idle-ratio`** — above this over-provisioning factor, capsize declines
  to prescribe a number at all, and says why. An instant sample of an idle
  workload is not a sizing basis. A contradiction computed from a
  recommendation I declined to make is not a finding either, so those are
  suppressed too.

**The risk score is never clamped by either.** It derives from declared
requests and node ceilings and is correct as it stands. These bounds apply
only to recommendations and to the what-if projections attached to them.

As with the other defaults, 32Mi and 50x are not derived from anything. They
are values that produce plausible advice on the clusters I have run this
against.

## 7. The formula lives in exactly one function

`risk.Compute(ceiling, request, neighbors, spot)` is the only place the
arithmetic is written down. Live scoring calls it, and so does
`Score.Project`, which prices a proposed change.

This is deliberate structure against a specific bug: a recommendation being
evaluated with different arithmetic than the finding that prompted it. If the
projection had its own copy of the formula, the two could drift and nothing
would catch it. With one function they cannot.

## 8. CAP301 derives from recommendations rather than recomputing them

The contradiction finding is built from the recommendations already produced,
not rediscovered from the inventory. The contradiction and the recommendation
therefore cannot disagree, by construction.

## 9. Effective resources are resolved before scoring

This one is a worked example rather than an argument, because it is the
strongest case in this document for testing against a real API server.

Kubernetes defaults a container's requests to its limits when limits are set
and requests are omitted. It is core API behavior — not a LimitRange, no
admission plugin — and it is invisible in the `PodTemplateSpec`.

capsize originally read the template verbatim. A workload declaring
`limits: {memory: 1Gi, cpu: "1"}` and no requests was scored as though it had
no reservation at all: the request fell back to `--request-floor`, giving a
ratio of 1Gi/10Mi and a risk two orders of magnitude too high, second riskiest
of sixteen workloads in my test cluster.

The cluster's own view of that pod was `Guaranteed` — requests defaulted to
the limits, the full 1Gi reserved, the *least* evictable QoS class there is.

`sandbox/limits-only` in the fixture is that workload, and the arithmetic is
checkable against the captured run in the README. Its true score is **2.32**:
ratio exactly 1.0, four neighbors, `log2(1 + 4) = 2.3219`. It ranks last.
Scored the old way the ratio would have been `1Gi / 10Mi = 102.4`, for
`102.4 × 2.3219 = 237.77` — the same 102x error on any host, because the two
scores share every term but the denominator.

That is worth stating precisely because it is the one figure on these pages
that is *not* in the capture: capsize cannot print it any more. It is derived
from numbers that are.

Every unit test was green throughout. They asserted on a model state that a
real cluster never produces. That is the failure mode: not a wrong assertion,
but a correct assertion about an impossible input. The fix was to resolve
effective resources at collection time, so nothing downstream can see a
pre-defaulting container; the regression guard is a table over all four
request/limit combinations driven from a real `PodSpec`.

## 10. Read-only is enforced structurally, in three layers

Not by convention, and not by a note in the README:

1. **A narrow client.** `internal/k8s` exposes only LIST and GET accessors.
   No other package can reach a mutating client method through the type
   system, because none is in scope.
2. **A transport that refuses.** Every request client-go makes, including
   discovery, passes through a RoundTripper that rejects any method other
   than GET, HEAD or OPTIONS.
3. **A build-breaking AST walk.** `internal/guard` parses every `.go` file in
   the module and fails the build on a write-shaped call or an unauthorised
   client-go import.

Layer three was verified by deliberately breaking it: I added a `.Update()`
call, watched the test fail with the file and line, and removed it. A guard
nobody has seen fail is a guard nobody knows works.

---

## The weak part: `neighbors` is an approximation

`neighbors` counts distinct workloads with pods **currently running** on a
node. The formula wants workloads **schedulable** on it.

Taints, `nodeSelector`s and anti-affinity rules make those two sets differ,
and I do not currently model any of them.

This is the only approximation in the formula, and it is the worst place to
have one, because the term multiplies every other term. A workload whose
neighbors are undercounted has its risk understated proportionally. Treat
the ranking as sound and the absolute values as indicative.

The related limit is scope: when capsize cannot list pods cluster-wide it
falls back to the namespace in scope, which understates neighbors further.
It says so in the output rather than quietly reporting a smaller number.

---

## What the numbers depend on

The ceiling term is node allocatable memory, so absolute scores move with the
size of your nodes. The relationships hold regardless — bounded versus
unbounded, spot versus on-demand, crowded versus quiet. Compare ratios, not
absolutes, across clusters.
