# Changelog

Notable changes, newest first. Dates are the release date; `Unreleased` is
what is on `main`.

capsize is pre-1.0. The `--json` document has its own version, independent of
these — see [`docs/json-contract.md`](docs/json-contract.md).

## Unreleased

### Changed
- The commit identity and trailer policy documentation was consolidated and
  reworded. No gate, workflow or hook changed behaviour.

## [v0.2.0] — 2026-09-05

JSON schemaVersion: **1.0.0** (first versioned shape).

The headline is that the report can now be trusted to reconcile with itself, and
that a denied read is reported as a denied read rather than as an absent finding.
Behind that, the repository grew the gates that catch this class of defect: a
four-node fixture cluster on every push, a lint and vulnerability gate, and a
pinned, reproducible release path.

### Fixed

- The human report claimed more findings, and more criticals, than it showed.
  CAP301 is counted in the summary's severity tally but printed in its own
  CONTRADICTIONS section, so a scan with one contradiction rendered
  `FINDINGS (8)` above a summary reading `9 finding(s): 4 critical` with three
  criticals visible. Contradictions now carry a severity tag like every other
  finding, and the FINDINGS heading reconciles with the total.
- A namespace whose LimitRange or ResourceQuota list could not be read was
  reported as having neither — turning an RBAC gap into a fabricated CAP201.
  Those namespaces are now excluded from the guardrail check, and the denial
  is reported as a warning.
- Denied `jobs` and `cronjobs` lists were silently ignored, so a cronjob
  workload could vanish from a report with nothing said. Both are now
  reported, like every other denied read.
- The warning for a denied `replicasets` list described the wrong fallback.

### Added

- `schemaVersion`, `toolVersion` and `generatedAt` on the `--json` envelope,
  with the compatibility rules written down in `docs/json-contract.md` and
  enforced by `TestJSONContract`.
- `deploy/rbac/capsize-readonly.yaml`: capsize's complete permission set,
  `list` only, one rule per call site. `docs/rbac.md` says what each missing
  permission costs, and `TestEveryDeniedReadIsReported` holds that table to
  the code.
- The README's terminal block, `docs/scan.svg` and every figure quoted in
  prose are re-captured from one run and now show the reconciled output. The
  absolute scores changed with the host they were captured on, which is the
  point the caption now makes explicitly.
- `docs/capture.sh`, which regenerates `docs/scan.svg`, `docs/scan.txt` and
  the README's terminal block from **one** capsize run. `--check` runs in CI
  and fails if the picture, the text and the figures quoted in prose stop
  agreeing — the drift that produced the bug above.
- `SECURITY.md`, `CONTRIBUTING.md`, this file, and `ROADMAP.md`.

### Changed

- The end-to-end job is reproducible rather than merely automated:
  metrics-server is vendored at v0.9.0 with its checksum verified and its
  image pinned by digest, the kind node image is pinned by digest, the kind
  version is explicit, `govulncheck` is pinned, and every action is pinned by
  commit SHA.
- Build stamps moved from `cmd` to `internal/buildinfo` so the JSON report can
  name the tool that wrote it. Release ldflags updated to match.
- The architecture diagram is redrawn. Mermaid's style directives take one fixed
  colour per node, so the old dark-only palette read as heavy black blocks on a
  light page; the replacement expresses both palettes behind
  `prefers-color-scheme`, which a style directive cannot do. The redraw also
  drops the specific blast-radius figures it used to end on, which were true of
  one host and read as universal.
- `docs/capture.sh` runs outside an interactive macOS shell. `script(1)` needs a
  controlling terminal and failed on a runner with a partial file, surfacing
  several steps later as a stray control character; a `python3` pty fork does
  the same job with no tty and no BSD/GNU argument split.
- The README shows the end-to-end gate and the prebuilt binaries. Both already
  existed; Install offered only `go install`, which needs the Go toolchain the
  binaries exist to avoid.

### Security

- CI gates on `golangci-lint` (errcheck, govet, staticcheck, ineffassign, unused,
  revive, gosec, unconvert, misspell). Its 27 findings were fixed rather than
  suppressed, and without moving a test expectation. The action is pinned so a
  lint release cannot redden a green branch.
- A `govulncheck` job, run against `./...` so it reports only vulnerabilities on
  a call path capsize actually reaches rather than every advisory in the
  client-go dependency tree.
- Dependabot on gomod and github-actions, weekly. The `k8s.io/*` modules are
  grouped because they move together and only merge as a set.
- Actions pinned by commit SHA, and `actions/checkout` and `actions/setup-go`
  moved to v7. `goreleaser-action` is deliberately left at v6: the release path
  is the one thing here with no test covering it, and bumping it belongs in a
  change where a tag can exercise it.
- An identity gate. `.githooks/pre-push` is committed so it travels with a
  clone, `make init` installs it, and an `identity` CI job runs the same file
  over all history — because `core.hooksPath` is per-clone configuration that a
  clone does not set. `.githooks/selftest.sh` proves the gate still rejects each
  thing it claims to reject.

### Test infrastructure

- The capsize-fixture cluster runs as an automated gate on every push, not a
  manual step: the workflow spins four nodes, installs metrics-server, waits for
  the ballast to settle and a scrape to land, then asserts an oracle of sixteen
  assertions. Every assertion is on a count or a value parsed from `--json`,
  never on an exit code or the absence of an error — a scan that silently found
  nothing would satisfy those and fail these.
- The scan waits for real steady state before running. A run that scanned while
  the ballast was still burning CPU produced a false CAP109 on the known-good
  workload; the wait now requires memory up and CPU back to idle across two
  consecutive polls, and fails loudly with the poll count rather than scanning
  something half-built.
- The spot-tenant assertion was corrected to the invariant it meant. Ranking
  `batch/spot-tenants` first held on a laptop-sized cluster and not on a 16GB
  runner, because a workload declaring no memory request is scored against
  `--request-floor` and its ratio follows node size. capsize was right in both
  runs; the oracle was wrong.

## [v0.1.1] — 2026-08-27

### Changed

- **Breaking, `--json`:** the `neighbours` field was renamed to `neighbors`.
  The repository standardized on American spelling and `misspell` now enforces
  it. This rename, made with no version on the document to bump, is why
  `schemaVersion` exists.

## [v0.1.0] — 2026-08-27

First release. Blast-radius scoring, the CAP1xx/CAP2xx detections, the CAP301
contradiction, `--json`, the `--fail-on` and `--risk-threshold` CI gates, and
prebuilt binaries for darwin and linux on amd64 and arm64.

[v0.2.0]: https://github.com/bezilla/capsize/releases/tag/v0.2.0
[v0.1.1]: https://github.com/bezilla/capsize/releases/tag/v0.1.1
[v0.1.0]: https://github.com/bezilla/capsize/releases/tag/v0.1.0
