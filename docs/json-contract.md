# The `--json` contract

`capsize -A --json` is advertised for automation, so its shape is a promise
rather than an implementation detail. This page is what that promise covers.

Every report carries an envelope:

```json
{
  "schemaVersion": "1.0.0",
  "toolVersion": "v0.1.0",
  "generatedAt": "2026-09-05T04:12:07Z",
  "context": "kind-capsize",
  "scope": "all namespaces",
  ...
}
```

- **`schemaVersion`** — the shape of the document, independent of the release
  that produced it. Read this, not `toolVersion`, to decide whether your
  parser understands the file.
- **`toolVersion`** — the capsize that wrote it: a release tag, the module
  version for a `go install`ed binary, or `dev` for a local build. Never
  empty; a blank version in a stored report is worse than an honest `dev`.
- **`generatedAt`** — RFC 3339, UTC. A risk score is a statement about a
  cluster at an instant, and a stored report with no timestamp cannot be aged.

## What the version means

`MAJOR.MINOR.PATCH`, and the only part a consumer needs to branch on is MAJOR.

| Change | Bump | Safe for an existing consumer |
|---|---|---|
| a field is added | MINOR | yes — ignore what you do not know |
| a field's meaning is clarified, values unchanged | PATCH | yes |
| a field is removed, renamed, or changes type | MAJOR | **no** |
| a field's units change (bytes → MiB, say) | MAJOR | **no** |
| an enum gains a value (a new `CAP` rule, a new `ceilingSource`) | MINOR | yes, if you treat unknown values as unknown rather than as an error |

Values are not part of the contract. Scores move with node size by design —
the same workload scores differently on a 2.6Gi node and a 6.55Gi one — so
pin your assertions to orderings and ratios, the way
`test/e2e/assert_oracle.py` does. A number changing is capsize working.

`1.0.0` is the first versioned shape. Reports from before this existed carry
no `schemaVersion` at all; treat an absent field as "older than 1.0.0" and
note that `neighbours` was renamed to `neighbors` in that era, which is a good
part of why this file exists.

## How the shape is defended

`internal/output/testdata/schema-<version>.json` records every path in the
document and the JSON type at it. `TestJSONContract` rebuilds a deliberately
maximal report — one that reaches every field, including the ones behind
`omitempty` — reduces it to that same path/type set, and diffs.

The schema file is named after the version, so the test cannot be satisfied by
regenerating it in place after an accidental change: adding a field means
either the diff fails, or `SchemaVersion` moves and a new file appears in the
review. Deliberately:

```
# 1. bump SchemaVersion in internal/output/report.go
# 2. record the new shape
go test ./internal/output -run TestJSONContract -update
# 3. say what changed, here and in CHANGELOG.md
```

## Two shapes worth knowing about

**`namespaces[]` is not every namespace.** It lists only the ones with neither
a LimitRange nor a ResourceQuota, which is why `limitRanges` and
`resourceQuotas` are always `null` in it — a namespace appears there precisely
because both are empty. A namespace whose guardrails could not be *read* is
absent rather than listed, and the reason is in `warnings[]`.

**`findings[]` includes contradictions; the table's FINDINGS section does
not.** CAP301 gets its own section in the human output, so the rendered list
is shorter than `counts.total`. In JSON there is one list and no such split —
filter on `rule == "CAP301"` if you want them separately.

## Exit codes

Unchanged by any of this, and the more stable interface of the two:

| Code | Meaning |
|---|---|
| 0 | the scan ran and nothing crossed a threshold you set |
| 1 | capsize failed — bad flags, unreachable API server, denied pod list on a `-A` scan |
| 2 | the scan succeeded and `--fail-on` or `--risk-threshold` was crossed |

A CI job that wants to distinguish "capsize broke" from "capsize found
something" should read the exit code, and one that wants to know *what* it
found should read this document.
