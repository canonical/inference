# Inference Manager

Inference Manager provides a management interface for local inference on Ubuntu.
The `inference` command-line interface (CLI) allows users to interact with and manage local inference models and providers.

A background service runs a proxy that provides a central entry point for local inference, such as listing available models and routing inference requests.
The inference proxy uses `http://localhost:8400/v1` as its entry point by default.

## Usage

Install Inference Manager as a snap:
```console
sudo snap install inference
```

> [!IMPORTANT]
> The snap is currently not available in the store. Refer to the [Local development](#local-development) section for instructions on building and installing the snap locally.

### Manage providers

To list available providers, run:
```
$ inference providers
PROVIDER                TYPE            STATE        
deepseek-r1             inference-snap  not installed
gemma3                  inference-snap  not installed
gemma4                  inference-snap  enabled      
glm-4-7-flash           inference-snap  not installed
glm-ocr                 inference-snap  not installed
nemotron-3-5-lightning  inference-snap  not installed
nemotron-3-nano         inference-snap  not installed
nemotron-3-nano-omni    inference-snap  not installed
nomic-embed-text-v1-5   inference-snap  not installed
qwen-vl                 inference-snap  not installed
qwen3                   inference-snap  enabled      
qwen3-5                 inference-snap  not installed
qwen3-6                 inference-snap  not installed
qwen3-8                 inference-snap  not installed
qwen3-coder             inference-snap  not installed
smollm2                 inference-snap  enabled      

Hint: run "inference install <provider>" to install providers.
```

The command lists all installed providers, along with any inference snaps that are available but not installed.

Use `inference install` to install a provider, `inference remove` to remove a provider.
At the moment, the commands only allow management of [inference snaps](https://github.com/canonical/inference-snaps).
Support for other types of providers, including remote providers, is planned for future releases.

Additional commands, including `inference enable` and `inference disable`, are under development. These commands will allow users to manage the state of installed providers.

### List models

To list available models, run:
```
$ inference models
ID
gemma4/gemma4-e2b    
smollm2/smollm2-135m 
```

This is the list of models available for inference, through the inference proxy:
```
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
```
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
