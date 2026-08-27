# Vendored fixture

These manifests are a copy. The source of truth is
**[github.com/bezilla/capsize-fixture](https://github.com/bezilla/capsize-fixture)**,
which carries the expected-findings table, the node topology rationale and the
`make` targets for driving the cluster by hand.

They are vendored here so the end-to-end job is hermetic: CI should not fail
because another repository moved. When the upstream fixture changes in a way
that matters to the oracle, copy the manifests across and update the
assertions in `.github/workflows/e2e.yml` in the same commit.

`90-balloon.yaml` is deliberately not vendored. It exists upstream to
demonstrate a live OOM eviction, and it has no place in an automated gate.
