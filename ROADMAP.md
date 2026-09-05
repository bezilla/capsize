# Roadmap

What capsize does not do yet, and why each one would matter. Nothing here is
committed to a date; this is a record of what has been considered, so that
"capsize doesn't do X" and "capsize will never do X" stay distinguishable.

Shipped work is in [`CHANGELOG.md`](CHANGELOG.md). The reasoning behind what
capsize already computes is in [`DESIGN.md`](DESIGN.md).

## Near term

- **`--explain <workload>`** — print the formula with this workload's numbers
  substituted. A score is only useful if it can be argued with, and right now
  arguing with one means reading `internal/risk`.
- **`--baseline <report.json>`** — diff against a stored report so CI fails on
  newly introduced risk rather than on pre-existing debt. Without it, the
  `--fail-on` gate is unusable on any cluster that is not already clean, which
  is every cluster that needs it.
- **Scheduling-aware neighbors** — taints, `nodeSelector`s and anti-affinity
  make "pods currently on this node" differ from "workloads schedulable on
  it". It is the only approximation in the formula and it multiplies every
  other term, so it is the single largest source of error in the score.
- **`--since` over a metrics window** — usage comes from metrics-server, which
  serves an instant. capsize takes the busiest pod rather than the mean to
  avoid sizing off the quietest replica, but one sample is still one sample,
  and a rightsizing recommendation deserves a window.

## Integrations

- **Historical metrics from Prometheus** — the same problem as `--since`, from
  the other end: most clusters that would benefit already store weeks of
  usage, and reading it would turn "over-provisioned right now" into
  "over-provisioned at p99 over a fortnight".
- **SARIF output** — would put findings in GitHub code scanning and every
  other tool that already speaks it, instead of requiring a bespoke consumer
  of `--json`.
- **OpenCost / cloud pricing join** — cost is currently measured in bytes of
  reservation. Priced in dollars, a contradiction becomes "this saves $340 a
  month and multiplies your blast radius by ten", which is the sentence that
  actually settles the argument.

## Distribution

- **A Homebrew tap** — `brew install` is the difference between someone trying
  capsize and someone bookmarking it. Release binaries exist; the tap does
  not.
- **A container image on GHCR** — for running capsize as a CI step or a
  CronJob without a Go toolchain or a downloaded binary.

## Not planned

- **Writing to clusters.** Not a feature that has been deferred; a property
  that is enforced three ways and tested. See [`SECURITY.md`](SECURITY.md).
- **A server, an agent, or an account.** capsize is a CLI that uses the
  kubeconfig you already have.
