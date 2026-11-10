![Inference Manager Banner](./snap/gui/inference-manager-banner.png)

# Inference Manager
[![inference](https://snapcraft.io/inference/badge.svg)](https://snapcraft.io/inference)

Discover, install and connect to inference models on Ubuntu — from one place.

Inference Manager brings provider management and model access together.
Use the command line to manage [inference snaps](https://documentation.ubuntu.com/inference-snaps), and connect your applications to a single endpoint that routes requests to the providers running on your machine.

Inference Manager allows you to:
* Discover available inference providers and inspect their models and engines.
* Install and remove inference snaps from the command line.
* List models available through the inference proxy.
* Check service status and provider health.
* Send inference requests through a single local endpoint.

## Get started

Install Inference Manager:
```console
sudo snap install inference
```

Find local providers:
```console
$ inference find gemma
NAME               CAPABILITIES                 
gemma3             text, vision                 
gemma4             text, thinking, tools, vision
nemotron-3-5-bolt  text, thinking, tools, vision
```

Add a local provider:
```console
$ inference install gemma4
```

List models:
```console
$ inference models
PROVIDER    MODEL     
clef-flash  clef-flash-9b
gemma4      gemma4-e4b
```

Integrate with your agents:
```console
$ curl -X POST http://localhost:8400/v1/chat/completions -H "Content-Type: application/json" -d '{
    "model": "gemma4/gemma4-e4b",
    "messages": [
      {
        "role": "user",
        "content": "What is the capital of France?"
      }
    ],
    "temperature": 0.7,
    "max_tokens": 100
  }'
```

The inference is optimized for the hardware:
```console
$ inference info gemma4
gemma4 [snap] - enabled ✓

API: http://127.0.0.1:8400/v1 (proxy)
     └── http://localhost:8336/v1 (upstream)

ENGINE         gemma4-e2b  gemma4-e4b  gemma4-12b  gemma4-26b-a4b 
amd-gpu        ✓           ✓           ✓           ✓              
cpu            ✓           ✓           ✓           ✓              
intel-cpu                  ✓                                      
intel-gpu                  ✓                                      
intel-onemkl   ✓           ✓           ✓           ✓              
nvidia-gpu     ✓           ✓           ✓           ✓          
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
