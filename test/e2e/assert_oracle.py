#!/usr/bin/env python3
"""Assert capsize's output against the fixture oracle.

Reads `capsize -A --json` on stdin or from a path. Every assertion is on a
count or a parsed value: never on an exit code, and never on the absence of
an error message. A scan that silently produced nothing would pass those and
fail these.
"""

import json
import math
import sys

GI = 1 << 30

# Workloads the fixture creates, by "namespace/name".
FIXTURE = {
    "prod/good-api",
    "sandbox/legacy-worker",
    "sandbox/orphan-job",
    "sandbox/limits-only",
    "sandbox/overprovisioned-cache",
    "batch/spot-tenant-a",
    "batch/spot-tenant-b",
    "batch/spot-tenant-c",
}
SPOT_TENANTS = {"batch/spot-tenant-a", "batch/spot-tenant-b", "batch/spot-tenant-c"}

failures = []


def check(ok, label, detail=""):
    print(f"  {'PASS' if ok else 'FAIL'}  {label}" + (f"  [{detail}]" if detail and not ok else ""))
    if not ok:
        failures.append(f"{label}: {detail}")


def main() -> int:
    raw = open(sys.argv[1]).read() if len(sys.argv) > 1 else sys.stdin.read()
    report = json.loads(raw)

    rows = {f"{r['ref']['namespace']}/{r['ref']['name']}": r for r in report["workloads"]}
    findings = {}
    for f in report["findings"]:
        ref = f.get("ref")
        if ref:
            findings.setdefault(f"{ref['namespace']}/{ref['name']}", []).append(f)

    print("\n=== observed ===")
    print(f"  metricsAvailable: {report.get('metricsAvailable')}  "
          f"note: {report.get('metricsNote', '')}")
    print(f"  workloads reported: {len(rows)}  findings: {len(report['findings'])}  "
          f"hidden system: {report.get('hiddenSystemWorkloads', 0)}")
    for name, r in sorted(rows.items(), key=lambda kv: -kv[1]["score"]["risk"]):
        s = r["score"]
        rules = sorted({f["rule"] for f in findings.get(name, [])})
        print(f"  {s['risk']:10.4f}  ratio={s['ratio']:8.3f}  nbrs={s['neighbors']:<3} "
              f"req={s['requestBytes']:<12} assumed={str(s['requestAssumed']):<5} "
              f"{name:32} {','.join(rules)}")

    print("\n=== preconditions ===")
    check(report.get("metricsAvailable") is True,
          "metrics-server reachable",
          report.get("metricsNote", "") or "usage-based rules would be silently skipped")
    missing = FIXTURE - set(rows)
    check(not missing, f"all {len(FIXTURE)} fixture workloads present",
          f"missing {sorted(missing)}" if missing else "")
    if missing:
        return report_failures()

    print("\n=== 1. false-positive control: prod/good-api has zero findings ===")
    # good-api declares correct requests and limits on both resources and
    # sits at 1.3x memory usage, so nothing should fire. A CAP109 here almost
    # always means the scan ran before the ballast dd finished and CPU fell
    # back to idle; the workflow's steady-state wait exists to prevent that.
    ga = findings.get("prod/good-api", [])
    check(len(ga) == 0, "prod/good-api finding count is 0",
          f"got {len(ga)}: {[f['rule'] for f in ga]}")

    print("\n=== 2. sandbox/limits-only: requests defaulted from limits ===")
    lo = rows["sandbox/limits-only"]["score"]
    check(lo["requestBytes"] == GI, "effective request is 1Gi",
          f"got {lo['requestBytes']}")
    check(lo["requestAssumed"] is False, "request is measured, not assumed",
          f"requestAssumed={lo['requestAssumed']}")
    check(lo["ceilingSource"] == "container limit", "ceiling comes from the limit",
          f"got {lo['ceilingSource']!r}")
    check(abs(lo["ratio"] - 1.0) < 1e-9, "ratio is exactly 1.0", f"got {lo['ratio']}")
    # risk = ratio x log2(1 + neighbors); with ratio 1.0 and 4 neighbors on the
    # ondemand-large node this is log2(5) = 2.3219.
    expected = math.log2(1 + lo["neighbors"])
    check(abs(lo["risk"] - expected) < 1e-6,
          f"risk equals log2(1+{lo['neighbors']}) = {expected:.4f}", f"got {lo['risk']:.4f}")
    check(abs(lo["risk"] - 2.3219) < 0.01, "risk is ~2.32",
          f"got {lo['risk']:.4f} with {lo['neighbors']} neighbors")

    print("\n=== 3. batch/spot-tenants rank #1 among fixture workloads ===")
    #
    # Among workloads that declare a memory request. A workload declaring none
    # is scored against --request-floor, so its ratio is set by how large the
    # node is rather than by anything it asked for: on a laptop-sized kind
    # node sandbox/orphan-job scores below the spot tenants, and on a
    # 16GB runner it scores well above them. That is capsize behaving
    # correctly in both cases, and it makes "rank #1 overall" a property of
    # the host rather than of the fixture.
    #
    # So the assertion is the part that is invariant: nothing with a measured
    # request outranks a spot tenant. The exclusion is made on requestAssumed,
    # not by name, so a workload that started being measured would be pulled
    # back into the comparison automatically.
    ranked = sorted(FIXTURE, key=lambda n: -rows[n]["score"]["risk"])
    measured = [n for n in ranked if not rows[n]["score"]["requestAssumed"]]
    check(measured and measured[0] in SPOT_TENANTS,
          "highest-risk workload with a declared request is a spot tenant",
          f"ranking: {[(n, round(rows[n]['score']['risk'], 2)) for n in measured[:3]]}")

    top_spot = max(rows[n]["score"]["risk"] for n in SPOT_TENANTS)
    above = [n for n in FIXTURE if rows[n]["score"]["risk"] > top_spot]
    check(all(rows[n]["score"]["requestAssumed"] for n in above),
          "anything outranking the spot tenants is there on an assumed request",
          f"outranking with a measured request: "
          f"{[n for n in above if not rows[n]['score']['requestAssumed']]}")
    spot_risks = {round(rows[n]["score"]["risk"], 6) for n in SPOT_TENANTS}
    check(len(spot_risks) == 1, "all three spot tenants score identically",
          f"got {spot_risks}")
    check(all(rows[n]["score"]["spot"] for n in SPOT_TENANTS),
          "spot tenants are detected as spot capacity")

    print("\n=== 4. sandbox/overprovisioned-cache: the contradiction ===")
    oc = sorted({f["rule"] for f in findings.get("sandbox/overprovisioned-cache", [])})
    for rule in ("CAP102", "CAP107", "CAP301"):
        check(rule in oc, f"{rule} present", f"got {oc}")
    contradictions = [f for f in report["findings"] if f["rule"] == "CAP301"]
    check(len(contradictions) >= 1, "at least one contradiction reported",
          f"got {len(contradictions)}")
    for f in contradictions:
        rec = f.get("recommendation") or {}
        check(rec.get("proposed", 0) >= 32 << 20,
              "no recommendation below the 32Mi floor",
              f"{f['ref']['name']} proposed {rec.get('proposed')}")

    return report_failures()


def report_failures() -> int:
    print()
    if failures:
        print(f"=== {len(failures)} assertion(s) failed ===")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("=== 0 assertions failed ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
