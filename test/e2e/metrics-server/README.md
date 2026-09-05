# Vendored metrics-server

`components.yaml` is a byte-for-byte copy of the manifest published with
metrics-server **v0.9.0**:

    https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml
    sha256  1cec29a5267809306a2c6ec74a3e449abbb705b4a8beed0c8a1963910f72c79b

It is vendored, not fetched, because the end-to-end job claims to be
reproducible. `releases/latest/download/components.yaml` is moving remote
code: the same commit could pass on Monday and fail on Tuesday because
somebody else cut a release, and nobody would be able to tell that apart from
a regression in capsize.

`components.yaml.sha256` is checked in CI before the manifest is applied, so
an edit to the vendored copy has to be a deliberate one that also updates the
checksum.

The container image inside the manifest is referenced by tag. A tag can be
re-pushed, so the workflow additionally patches the deployment to the digest
that tag resolved to when this was vendored:

    registry.k8s.io/metrics-server/metrics-server:v0.9.0
    sha256:d9862115e7c7881280d3d75ca26bda8ffc0fc213315979575bf23ce9826205c0

## Refreshing it

    VERSION=v0.9.1
    curl -sSLo components.yaml \
      "https://github.com/kubernetes-sigs/metrics-server/releases/download/$VERSION/components.yaml"
    shasum -a 256 components.yaml > components.yaml.sha256
    docker buildx imagetools inspect \
      "registry.k8s.io/metrics-server/metrics-server:$VERSION"   # -> new digest

Then update `METRICS_SERVER_IMAGE` in `.github/workflows/e2e.yml` and the two
digests above in the same commit.
