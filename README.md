# capsize

**A read-only Kubernetes CLI that scores cost waste and blast radius at the same time — and tells you where fixing one makes the other worse.**

---

Two production clusters went down an hour each. Every dashboard we had said everything was fine right up until it wasn't. The cause was three harmless things stacking: no resource limits anywhere (years older than me), a move to spot autoscaling that changed node packing, and a memory leak in the Node version we ran. One pod could eat a whole node, nothing capped it, and the autoscaler kept packing more onto nodes that were already starving.

Every cost tool I've used since would have told me to shrink my requests. None of them would have told me the limits were missing.

That's what this does.

---

## The idea in one output block

```
sandbox/overprovisioned-cache (Deployment, risk 12.2)
  cutting the memory request from 512Mi to 51Mi releases 461Mi per pod but raises blast
  radius 10x (12.2 -> 122.6): with no memory limit set, the request is the only number
  holding this workload below 2.6Gi of allocatable memory on capsize-worker2, which it
  shares with 4 other workloads. Do this first: set a memory limit.
```

Both halves are true. The savings are real — that workload requests 512Mi and uses 40Mi. And acting on them, alone, makes an outage more likely, because the request is the only thing keeping an unbounded container off the rest of the node.

Cost optimization and blast-radius containment can pull in **opposite directions**. Every tool in this category treats them as one axis. capsize scores both and names the disagreement.

## Install

```bash
go install github.com/bezilla/capsize@latest
```

No agent, no cluster-side install, no account, no credentials beyond the kubeconfig `kubectl` already uses.

## Use

```bash
capsize                      # current namespace
capsize -A                   # every namespace you can read
capsize -A --top 10          # the ten riskiest workloads
capsize -A --json            # full report, machine-readable
capsize -A --fail-on warn    # exit 2 in CI if anything is warn or worse
```

Scanning one namespace of the [test fixture](#testing) — a `kind` cluster built to be broken in specific ways:

```
$ capsize -n sandbox
capsize  context kind-capsize  scope namespace sandbox
usage data: metrics-server

! 1 cost recommendation(s) here would increase blast radius

BLAST RADIUS
  RISK   RATIO  NBRS  SPOT  CEILING   REQUEST  KIND        WORKLOAD                       NODE             FLAGS
  128.9  64x    3     -     644.4Mi*  ~10Mi    Deployment  sandbox/orphan-job             capsize-worker   2 finding(s)
  12.2   5.3x   4     -     2.6Gi*    512Mi    Deployment  sandbox/overprovisioned-cache  capsize-worker2  contradiction 3 finding(s)
  5.03   2.5x   3     -     644.4Mi*  256Mi    Deployment  sandbox/legacy-worker          capsize-worker   1 finding(s)
  2.32   1.0x   4     -     1Gi       1Gi      Deployment  sandbox/limits-only            capsize-worker2  2 finding(s)

CONTRADICTIONS (1)
  sandbox/overprovisioned-cache (Deployment, risk 12.2)
    cutting the memory request from 512Mi to 51Mi releases 461Mi per pod but raises blast
    radius 10x (12.2 -> 122.6): with no memory limit set, the request is the only number
    holding this workload below 2.6Gi of allocatable memory on capsize-worker2, which it
    shares with 4 other workload(s). Do this first: set a memory limit (nothing currently
    bounds this below 2.6Gi of node capsize-worker2).

FINDINGS (8)
  critical CAP102  sandbox/orphan-job/app  no resource limits
    declares no CPU or memory limit, so its memory ceiling is the whole of capsize-worker
    (644.4Mi) and it shares that node with 3 other workload(s)
  critical CAP102  sandbox/overprovisioned-cache/app  no resource limits
    declares no CPU or memory limit, so its memory ceiling is the whole of capsize-worker2
    (2.6Gi) and it shares that node with 4 other workload(s)
  critical CAP102  sandbox/legacy-worker/app  no resource limits
    declares no CPU or memory limit, so its memory ceiling is the whole of capsize-worker
    (644.4Mi) and it shares that node with 3 other workload(s)
  warn     CAP101  sandbox/orphan-job/app  no resource requests
    declares neither requests nor limits, so the scheduler treats it as free and packs it
    onto any node with room; the pod is BestEffort and is evicted first
  warn     CAP107  sandbox/overprovisioned-cache  memory request far above observed usage
    requests 512Mi of memory but its busiest pod uses 40.4Mi (13x over-provisioned across 1
    replica(s)); a request of 51Mi would hold 1.2x headroom
  warn     CAP201  sandbox  namespace has no LimitRange and no ResourceQuota
    neither a LimitRange nor a ResourceQuota exists in sandbox, so a pod admitted here may
    declare nothing and consume everything; 4 workload(s) currently rely on that
  info     CAP103  sandbox/limits-only/app  memory request left implicit
    limits memory to 1Gi and declares no memory request, so Kubernetes defaults the request
    to the limit: the scheduler reserves 1Gi and this pod is Guaranteed. The outcome is
    right but implicit - spell out requests.memory: 1Gi so the reservation cannot move
    silently when someone edits the limit
  info     CAP105  sandbox/limits-only/app  CPU request left implicit
    limits CPU to 1 and declares no CPU request, so Kubernetes defaults the request to the
    limit: the scheduler reserves 1 and this pod is Guaranteed. Spell out requests.cpu: 1 so
    the reservation cannot move silently when someone edits the limit

SUMMARY
  4 workload(s) scored; 9 finding(s): 4 critical, 3 warn, 2 info, 1 of which is a cost fix
  that raises blast radius
  highest blast radius: 128.9 (sandbox/orphan-job)
  * ceiling is the node's allocatable memory because no container limit bounds it
```

`orphan-job` outranks `overprovisioned-cache` ten to one, and that is the point. It declares nothing at all, so it is scored against `--request-floor` on the tightest node in the cluster — the scheduler treats it as free, packs it anywhere, and nothing caps what it can take. The workload that *looks* wasteful is the third-riskiest thing here.

## How the score works

```
ceiling  = min(node allocatable memory, container memory limit)
ratio    = ceiling / memory request
risk     = ratio x log2(1 + neighbours) x spot_factor        (spot_factor = 1.5)
```

**The ceiling term is the whole argument.** With no limit set, a container's ceiling is the entire node — so the *request* becomes the only number holding it back, and shrinking the request raises the ceiling ratio directly. Set a limit and the ceiling collapses to that limit. The metric rewards bounding a workload without needing a special case for it.

`log2` on neighbours because the second tenant on a node matters far more than the twelfth. `spot_factor` because preemption changes node packing, and changed packing is what turned a latent bug into an outage.

The formula is written down exactly once, in `internal/risk`. Live scoring and what-if projection both call it, so **a recommendation can never be priced with different arithmetic than the finding that prompted it.**

## What it checks

| Series | What it covers |
|---|---|
| **CAP1xx** | workload shape — missing requests, missing limits, limits without requests, requests far above observed usage |
| **CAP2xx** | namespace guardrails — no LimitRange and no ResourceQuota |
| **CAP301** | **the contradiction** — a cost recommendation that would raise blast radius |

CAP301 is derived from the recommendations rather than rediscovered independently, so the two can't disagree. It always names the guard to apply first: *set a memory limit* when none exists, *lower the limit alongside the request* when one does.

## Read-only, enforced in three layers

capsize never writes to your cluster. That's not a promise in a README, it's three mechanisms:

1. **`internal/k8s` exposes no method that writes.** Nothing else in the module can reach one through the type system.
2. **The HTTP transport rejects every verb but GET, HEAD and OPTIONS.**
3. **`internal/guard` parses every `.go` file in the module at test time** and fails the build on a write-shaped call or an unauthorised client-go import.

Layer three was verified by breaking it on purpose: a `.Update()` call was added, the build failed with the file and line, and it passed again once removed. A fourth test feeds the walker a synthetic write so it can't pass vacuously.

## Limitations

Written down because you should know them before you trust a number.

- **Neighbours means "workloads with pods currently on the node," not "workloads schedulable on it."** Taints, nodeSelectors and anti-affinity make those differ. This is the one term in the formula that is an approximation rather than a fact, and it multiplies everything else.
- **Usage comes from metrics-server, which serves an instant, not a window.** capsize takes the *busiest* pod rather than the mean, because sizing off the quietest replica is how a rightsizing becomes a page. It is still one sample. A `--since` window is on the roadmap.
- **A workload spread across nodes is scored against its worst node**, not the mean.
- **Tenancy is read cluster-wide even for `-n` scans** — a kube-system neighbour OOMs you just as dead. If that read is denied by RBAC, the neighbour count is reported as a stated lower bound rather than silently undercounting.
- **A workload with no memory request is scored against `--request-floor`** (10Mi default) so its ratio stays finite, and printed as `~10Mi` so you can see it was assumed.
- **No request recommendation is made below `--min-request`** (32Mi default), and above `--idle-ratio` (50x) capsize declines to prescribe a number at all — an instant sample of an idle workload is not a sizing basis, and "shrink to 1Mi" is arithmetic, not advice.
- **Cost is not yet priced in dollars.** Phase 2 joins node instance types against the public AWS pricing API.

## Testing

Unit tests cover the formula, the detectors and the read-only guard. They were all green while a real bug shipped.

So there is also a **[fixture](https://github.com/bezilla/capsize-fixture): a four-node `kind` cluster that is broken on purpose**, with differentiated node capacity, a spot pool, real memory consumers, and two false-positive controls that must produce **zero** findings. Its README carries an expected-findings table used as an oracle.

That fixture caught a genuine defect the unit tests could not: capsize read container resources straight from the PodTemplateSpec and never applied Kubernetes' own defaulting, so a workload declaring limits and omitting requests was scored as if it had no reservation at all. It was ranked second-riskiest of sixteen. It belonged last. The unit tests passed because they asserted on a state a real cluster never produces.

## Roadmap

- **Phase 1 — shipped.** Guardrails, blast radius, contradictions, JSON, CI gate.
- **Phase 1.5.** Scheduling-aware neighbours · `--explain <workload>` to print the formula with this workload's numbers substituted, because a score is only useful if it can be argued with · `--since` window over metrics · `--baseline` diff so CI fails on newly introduced risk rather than pre-existing debt.
- **Phase 2 — cost.** Public AWS pricing join. Waste priced, exposure priced, and every cost recommendation that raises blast radius called out in dollars.

## License

MIT.
