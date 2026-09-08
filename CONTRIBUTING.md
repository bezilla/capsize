# Contributing

Issues and pull requests are welcome. This is a solo project, so the most
useful thing you can send is a case where capsize is **wrong** — a workload it
scored in a way you can argue with, or a finding that fired when it should not
have.

## Running it

```bash
go build ./...
go test ./...          # unit tests, including the read-only guard
golangci-lint run      # config in .golangci.yml
```

The end-to-end suite needs Docker and `kind`. It builds a four-node cluster
that is broken on purpose and asserts an oracle against `--json`:

```bash
kind create cluster --config test/e2e/fixture/kind-cluster.yaml --name capsize
kubectl apply -f test/e2e/metrics-server/components.yaml   # see that directory's README
kubectl apply -f test/e2e/fixture/00-namespaces.yaml
kubectl apply -f test/e2e/fixture/         # then wait for steady state
./capsize -A --json > report.json
python3 test/e2e/assert_oracle.py report.json
```

`.github/workflows/e2e.yml` is the authoritative version of that sequence,
including the steady-state wait that keeps the false-positive control from
becoming a coin flip.

## Three things that will fail CI

**Anything that writes to a cluster.** `internal/guard` walks every `.go` file
and fails the build on a write-shaped call or an unauthorized client-go
import. If that test fails on your branch, it is working. Read
[`SECURITY.md`](SECURITY.md) for why it exists.

**A change to the `--json` shape without a version bump.**
`TestJSONContract` compares the document against a schema file named after
`SchemaVersion`. The failure message tells you what to do;
[`docs/json-contract.md`](docs/json-contract.md) says what counts as a
breaking change.

**Docs quoting figures from two different runs.** The README's terminal block,
`docs/scan.txt`, `docs/scan.svg` and every figure quoted in prose come from one
`docs/capture.sh` run against the fixture. `docs/capture.sh --check` runs in CI
and reads committed files only, so it needs no cluster. Re-capture with:

```bash
./docs/capture.sh          # needs a live fixture cluster, freeze, and go
```

**Absolute scores asserted as fixed numbers.** They are host-specific: the
ceiling term is node allocatable memory, so the same workload scores 12.2 on a
2.6Gi node and 30.43 on a 6.55Gi one. Assert orderings and ratios, which are
the invariants — `test/e2e/assert_oracle.py` is the worked example.

## Commit identity, and the one exception

Every commit must be authored **and** committed from `bezilla@protonmail.com`.
The address is asserted **exactly** — that is the field that identifies a person,
and no repository setting stops a server-side merge from rewriting it.

The **name** beside it is checked against a two-entry list:

```
ACCEPTED_NAMES=('Paul Bezilla' 'pjbezilla')
```

That is a deliberate exception, not an oversight. Three early commits were made
as `pjbezilla` on the same address, and they are reachable from `v0.1.0` and
`v0.1.1`. Correcting the name means rewriting those commits, which changes their
hashes, which means deleting and recreating the tags — and that destroys three
published releases whose binaries and `checksums.txt` people have already
downloaded. A name that reads two ways is a smaller cost than a broken release,
so the history stands and the check accommodates it.

The exception is narrow in both directions, and the self-test asserts both:

- a name nobody here has used is **rejected**, even on the canonical address
- the canonical name on a **wrong address** is rejected — the list is not a way in
- `pjbezilla` on the canonical address is **accepted**, so a future tightening
  back to one entry fails a test rather than making this repository unpushable

A `Signed-off-by` trailer does **not** get the allowance. The author field on
three commits predates the gate; a sign-off is something you write deliberately
today, so it must carry `Paul Bezilla <bezilla@protonmail.com>` exactly.

## Commit trailers are allowlisted

Only three keys may appear in a commit's trailer block, or in an annotated tag's
annotation body. Every other key is refused:

| trailer | rule |
|---|---|
| `Signed-off-by` | must be exactly `Paul Bezilla <bezilla@protonmail.com>` |
| `Verified` | free text |
| `Measured` | free text |

A denylist can only refuse what somebody thought to write down and cannot be
completed; an allowlist refuses on the key, so an unlisted key is refused whether
or not the gate has heard of it.

### The trailer rule has one sharp edge

Whether a `Key: Value` line is a trailer depends on **which paragraph it lands
in**. git parses only the last paragraph, and only when the whole paragraph
parses as trailers:

```
Cap the ceiling term                 Cap the ceiling term

Verified: 3 runs, 0 failures.        Verified: 3 runs, 0 failures.

And a closing paragraph.             ← nothing after it
```

The left-hand message ends in prose, so `Verified:` there is ordinary text the
gate never looks at. The right-hand one ends with that line, so it **is** a
trailer and its key must be allowlisted. Same words, two outcomes, decided by
what comes after.

The gate reads trailers with `git interpret-trailers --parse` — git's own
definition. A `^Key:` regex would reject ordinary prose; six lines in this
repository are `Key: Value` shaped and are not trailers (`exposed:`, `docs:`,
`Verified:`, `Tests:`, `specific:`, `back:`).

If a push is refused for a trailer you thought was prose, check whether it ended
up last. A new evidence word needs adding to the allowlist first.

### What the allowlist does not catch, on purpose

Two things pass this gate that an earlier version of it would have stopped. Both
are the deliberate reduction, not an oversight.

**Anything in the body of a message.** The gate's scope is the trailer block: it
reads that and nothing else, so a `Key: Value` shape written in a paragraph of
prose is ordinary text and is accepted. Refusing on the key is what makes the
rule hold — an unlisted key is refused whether or not the gate has heard of it,
which a name list cannot promise, because it needs updating every time an
unanticipated name appears. Matching words in prose is a different job, and this
gate does not do it.

**Anything in the working tree.** Nothing greps the checkout.
Hand-written hooks under `.git/hooks/` once did, and `core.hooksPath` makes git
ignore that directory entirely, so any that survive there are inert. They have
not been restored and should not be: it is the same scan, and it walked build
artefacts, so a full validation run could leave a clean tree unpushable.

## Cutting a release

Push the tag. That is the whole procedure, and it is the only publish path.

```sh
git tag -a v0.3.0 -m "v0.3.0"
git push origin v0.3.0
```

`release.yml` fires on `v*`, builds the four binaries and uploads them with
`checksums.txt`. Nothing else publishes.

Locally, only ever the snapshot:

```sh
goreleaser release --snapshot --clean   # builds; touches GitHub not at all
./dist/capsize_darwin_arm64*/capsize version
```

**`goreleaser release` without `--snapshot` publishes for real** when a
`GITHUB_TOKEN` is in the environment, and it races the workflow the tag push has
already started. Both produce the same asset names on the same release, the
first to finish wins, and the second gets one of these per asset:

```
upload failed  error=POST https://uploads.github.com/repos/bezilla/capsize/releases/N/assets?name=capsize_0.2.0_linux_arm64.tar.gz: 422 Validation Failed [{Resource:ReleaseAsset Field:name Code:already_exists Message:}]
⨯ release failed  error=scm releases: failed to publish artifacts
```

Five 422s and a red X on a Release run whose build steps all passed. That is the
signature, and it happened on v0.2.0.

The other tell is the uploader, which says which machine won:

```sh
gh api repos/bezilla/capsize/releases/tags/v0.2.0 \
  --jq '.assets[] | "\(.name)\t\(.uploader.login)"'
```

CI uploads as `github-actions[bot]`. A local run uploads as a username.

The release survives this — the winner's assets are complete and verify against
`checksums.txt`, and the loser uploaded nothing. Leave the failed run red.
Clearing it means deleting good assets to re-upload them, trading a cosmetic X
for a window in which the release has no binaries at all.

`release.replace_existing_artifacts: true` would let the second run overwrite
instead of fail. It is deliberately unset: `buildinfo.Date` stamps wall-clock
build time, so the two runs' archives are not byte-identical, and whichever
finished last would quietly decide which machine's binaries ship.

## If you are changing the score

The formula lives in exactly one function, `risk.Compute`, so that a
recommendation can never be priced with different arithmetic than the finding
that prompted it. Keep it that way.

[`DESIGN.md`](DESIGN.md) records why each term is shaped the way it is,
including the parts I have no principled reason for. Disagreeing with one of
those is a good pull request; so is disagreeing in an issue without one.

## Style

Match the surrounding code. Comments explain *why*, not what — if a comment
restates the line below it, delete one of them. American spelling; `misspell`
enforces it.
