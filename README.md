# Inference Manager

Inference Manager provides a management interface for local inference on Ubuntu.
The `inference` command-line interface (CLI) allows users to interact with and manage local inference models and providers.

A background service runs a proxy that provides a central entry point for local inference, such as listing available models and routing inference requests.
The inference proxy uses `http://localhost:8400/v1` as its entry point by default.

## Usage

Install Inference Manager:
```console
sudo snap install inference
```

### Manage providers

To list providers, run:
```console
$ inference providers
PROVIDER  BACKEND  ENABLED
gemma4    snap     yes
qwen3     snap     yes
smollm2   snap     yes

Hint: Run "inference find" to discover other inference snaps.
```

To show a provider's state and available engines and models, run:
```console
$ inference info qwen3-5
qwen3-5 [snap] - not installed ✗

ENGINE               qwen3.5-0.8b  qwen3.5-2b  qwen3.5-4b  qwen3.5-9b  qwen3.5-35b-a3b
cpu                  ✓             ✓           ✓           ✓           ✓
nvidia-gpu           ✓             ✓           ✓           ✓           ✓
nvidia-jetson-orin   ✓             ✓           ✓           ✓
```
Use `--format=json` for machine-readable output.

It is possible to search for a specific provider by using the `inference find` command.
For example:

```console
$ inference find Gemma
NAME               CAPABILITIES                 
gemma3             text, vision                 
gemma4             text, thinking, tools, vision
nemotron-3-5-bolt  text, thinking, tools, vision

Hint: Run "inference info <name>" for details.
```

Use `inference install` to install a provider, `inference remove` to remove a provider.
At the moment, the commands only allow management of [inference snaps](https://github.com/canonical/inference-snaps).
Support for other provider backends, including remote providers, is planned for future releases.

Additional commands, including `inference enable` and `inference disable`, are under development. These commands will allow users to manage the state of installed providers.

### List models

To list available models, run:
```console
$ inference models
PROVIDER  MODEL
gemma4    gemma4-e2b
smollm2   smollm2-135m
```

This is the list of models available for inference, through the inference proxy:
```console
$ curl --silent http://localhost:8400/v1/models | jq
{
  "object": "list",
  "data": [
    {
      "id": "gemma4/gemma4-e2b",
      "object": "model",
      "created": 1789989775,
      "owned_by": "gemma4"
    },
    {
      "id": "smollm2/smollm2-135m",
      "object": "model",
      "created": 1789989775,
      "owned_by": "smollm2"
    }
  ]
}
```

### Show status

To check the status of the Inference Manager, run:
```console
$ inference status
services:
  inference.d: active
proxy:
  openai:
    base-url: http://localhost:8400/v1
health:
  gemma4: ok
  smollm2: ok
```

## Local development

Download the snap catalog and point the CLI at it with `INFERENCE_SNAPS_CATALOG`:

```console
curl -LO https://canonical.github.io/inference-snaps-admin/onboarded-snaps.json
export INFERENCE_SNAPS_CATALOG="$PWD/onboarded-snaps.json"
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
