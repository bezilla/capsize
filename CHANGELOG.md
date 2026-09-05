# Changelog

Notable changes, newest first. Dates are the release date; `Unreleased` is
what is on `main`.

capsize is pre-1.0. The `--json` document has its own version, independent of
these — see [`docs/json-contract.md`](docs/json-contract.md).

## Unreleased

JSON schemaVersion: **1.0.0** (first versioned shape).

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

[v0.1.1]: https://github.com/bezilla/capsize/releases/tag/v0.1.1
[v0.1.0]: https://github.com/bezilla/capsize/releases/tag/v0.1.0
