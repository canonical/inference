# Inference Manager

The `inference` snap is a management interface for Inference Snaps.

## Local development

Download the snap catalog and point the CLI at it with `INFERENCE_SNAPS_CATALOG`:

```console
curl -LO https://canonical.github.io/inference-snaps-admin/onboarded-snaps.json
export INFERENCE_SNAPS_CATALOG="$PWD/onboarded-snaps.json"
```

The CLI defaults to the confined snapd socket at `/run/snapd-snap.socket`. When
running outside the snap, point it at the host socket:

```console
export SNAPD_SOCKET=/run/snapd.socket
```

Build and run the CLI directly with Go:

```console
go build ./...
go run ./cmd/inference providers
```

Run the test suite:

```console
go test ./...
```

To build and install the snap locally:

```console
snapcraft pack -v
sudo snap install --dangerous ./inference_*.snap
```

The strictly confined snap requires the `network` and `snapd-control` interfaces.
A local development install may require:

```console
sudo snap connect inference:snapd-control
```
