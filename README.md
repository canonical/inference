# Inference Manager

The `inference` snap is a management interface for Inference Snaps.

## Local development

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
