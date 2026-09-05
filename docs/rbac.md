# What capsize needs, and what it does without

"No credentials beyond kubeconfig" is true and unhelpful: it says capsize
asks for nothing of its own, not what the identity behind that kubeconfig has
to be allowed to do. This page answers the second question.

The role is [`deploy/rbac/capsize-readonly.yaml`](../deploy/rbac/capsize-readonly.yaml).
Every rule in it maps to a call site in `internal/k8s/client.go`, and there is
no call site without a rule.

## The permission set

| Group | Resource | Verb | Why |
|---|---|---|---|
| core | `nodes` | `list` | allocatable memory is the ceiling term; labels detect spot capacity |
| core | `pods` | `list` | tenancy — who else is on the node |
| core | `namespaces` | `list` | the guardrail check covers empty namespaces too |
| core | `limitranges`, `resourcequotas` | `list` | CAP201 itself |
| apps | `deployments`, `statefulsets`, `daemonsets` | `list` | workloads are enumerated from controllers, so one scaled to zero is still scored |
| apps | `replicasets` | `list` | resolve Pod → ReplicaSet → Deployment |
| batch | `cronjobs` | `list` | workload enumeration |
| batch | `jobs` | `list` | resolve Pod → Job → CronJob |
| metrics.k8s.io | `pods` | `list` | observed usage |
| — | `/apis/metrics.k8s.io/v1beta1` | `get` | ask whether metrics-server exists before reading from it |

**The verb is `list`, never `get` or `watch`.** capsize issues no GET-by-name
and opens no watch, so those verbs would widen the role past anything the
binary can use. The single `get` is on a non-resource discovery URL.

`nodes` and `namespaces` are cluster-scoped, so those two rules cannot be
expressed as a Role in one namespace. Everything else can, at the cost
described below.

## What breaks, and how loudly

Every denial below is stated in the report — as a `warning:` line above the
table and in `warnings[]` in `--json`. That is the contract: a score computed
without the node list means something different from one computed with it, so
the gap travels with the number rather than being swallowed.

`internal/collect`'s `TestEveryDeniedReadIsReported` asserts this table
against real behavior, so this page cannot quietly drift from the code.

| Denied | What you still get | What you lose |
|---|---|---|
| `nodes` | every static finding (CAP101–CAP106), and cost findings if metrics are readable | **all blast-radius scoring.** No ceiling, no neighbors; every row is unscored. This is the cost half of the tool only |
| `pods` cluster-wide, scanning `-A` | nothing | the scan **fails**. There is no inventory to report, and reporting an empty cluster would be a lie |
| `pods` cluster-wide, scanning `-n ns` | a full report for that namespace | neighbor counts fall back to the namespace, so they are a **stated lower bound** and every risk score is understated |
| `namespaces` | guardrail checks on namespaces that already hold a visible workload | CAP201 on **empty** namespaces — which are the ones most worth knowing about, since the hole is there before anything falls through it |
| `limitranges` or `resourcequotas` | everything else | CAP201 entirely. Those namespaces are excluded from the check rather than reported as ungoverned: a denied read is not an absent guardrail, and reporting it as one would manufacture a critical finding out of an RBAC gap |
| `metrics.k8s.io` (either rule) | every static finding and **every blast-radius score** | CAP107/108/109 and, with them, CAP301 — the contradiction needs a rightsizing recommendation to contradict |
| `replicasets` | everything else | deployment attribution. Pods owned by a ReplicaSet are attributed to themselves as uncontrolled `Pod`s rather than to their Deployment, so one deployment reads as N single-pod workloads |
| `jobs` | everything else | cronjob attribution, the same way |
| `deployments` / `statefulsets` / `daemonsets` / `cronjobs` | the kinds you can read | that kind, entirely — its workloads are absent from the report |

## Scanning one namespace with a namespaced Role

Two things are worth knowing before you narrow it that far.

Blast radius is a property of a **node**, not of a namespace. Withholding
cluster-wide `pods` does not make the numbers smaller-but-fine; it makes them
understated by an unknown amount, because the neighbors capsize cannot see are
exactly the ones that would raise the score. capsize says so in the output
rather than quietly reporting the smaller number, but a stated lower bound is
still a lower bound.

Withholding `nodes` removes blast radius altogether. What is left is a
competent cost linter — which is most of what this category already ships, and
none of the reason capsize exists.

## Verifying it

    kubectl apply -f deploy/rbac/capsize-readonly.yaml
    kubectl create clusterrolebinding capsize \
      --clusterrole=capsize-readonly --user="$(whoami)"

    # every rule, without running capsize
    for r in nodes pods namespaces limitranges resourcequotas; do
      kubectl auth can-i list "$r" --all-namespaces --as="$(whoami)"
    done
    for r in deployments statefulsets daemonsets replicasets; do
      kubectl auth can-i list "$r.apps" --all-namespaces --as="$(whoami)"
    done
    kubectl auth can-i list jobs.batch --all-namespaces --as="$(whoami)"
    kubectl auth can-i list cronjobs.batch --all-namespaces --as="$(whoami)"
    kubectl auth can-i list pods.metrics.k8s.io --all-namespaces --as="$(whoami)"

A clean scan under that binding prints no `warning:` line. If it prints one,
the warning names the permission that is missing.

## What it cannot do

The role grants no write verb anywhere, but that is the weakest of capsize's
three read-only guarantees rather than the strongest — RBAC is enforced by
someone else's cluster, and the other two are enforced by this repository. See
[the README](../README.md#read-only-enforced-in-three-layers) and
[`SECURITY.md`](../SECURITY.md).
