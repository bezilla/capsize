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
