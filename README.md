# inference

**Silicon-optimized local AI orchestration for Ubuntu.**

`inference` is the front door to local AI on Ubuntu. Install it with one command and
it detects your hardware, advises on drivers, installs silicon-optimal builds of
inference engines and models, and federates every endpoint behind one litellm-style
OpenAI-compatible proxy — across edge, laptops, workstations, and clusters.

```bash
sudo snap install inference          # the orchestration layer
inference catalogue                  # browse models + what accelerates on your box
inference install gemma4             # silicon-optimal install
inference chat                       # talk to it (or point any OpenAI client at :8080/v1)
```

- **Design:** [DESIGN.md](DESIGN.md) — UX spec + as-built architecture
- **Roadmap:** [TODO.md](TODO.md)

---

## What it does

- **Hardware detection & profiling** — CPU/ISA, GPU (NVIDIA/AMD/Intel), NPU, memory;
  classifies the machine (edge / laptop / workstation / server) and tunes defaults.
- **Catalogue & install** — browse the official model snaps, see which accelerate on
  *your* silicon, and install with a typo-tolerant `+` grammar
  (`gemma4+cuda+webui`). Driver gaps are surfaced with the exact fix.
- **Federated proxy** — one OpenAI-compatible endpoint (`:8080/v1`) over every local
  model snap plus external providers (OpenAI/Anthropic), with name/alias routing.
- **Interactive CLI** — `inference chat` (REPL) and `inference run` (one-shot/pipe).

## Architecture (how it stays strictly confined)

snapd authorizes snap management by **uid**, so a confined non-root process cannot
read other snaps' config or install them. Therefore:

- **`inference.proxy` is a root daemon** (auto-started on install) — the single
  privileged broker. It discovers model snaps and installs/removes them via the snapd
  REST API (authorized by `snapd-control` + uid 0), and serves the federated proxy.
- **The CLI is unprivileged** and delegates management to the daemon over `localhost`
  (`/v1/backends`, `/v1/install`, `/v1/remove`), streaming progress back.
- **Discovery is dynamic** — any installed snap serving an OpenAI endpoint is
  auto-federated; you don't have to install through `inference`.

Strict confinement, plugging `network`, `network-bind`, `hardware-observe`,
`system-observe`, `snapd-control`. Single Go static binary, `core24` base.

---

## Build from source

Requires Go 1.26+ and (for the snap) `snapcraft` + LXD.

```bash
# binary (development)
go build -o inference ./cmd/inference
./inference catalogue

# the snap
snapcraft pack --use-lxd        # → inference_0.1.0_amd64.snap
```

In development the binary talks to snapd via the `snap` CLI; inside the packaged
snap the root daemon uses the snapd REST socket.

---

## Full lifecycle

Written as if installing **from the Snap Store**; local-build equivalents shown alongside.

### 1. Find & install

`inference` is strictly confined. `network`/`network-bind` auto-connect; the privileged
`hardware-observe`, `system-observe`, and `snapd-control` are connected explicitly.

```bash
# from the store
sudo snap install inference
sudo snap connect inference:snapd-control     # manage model snaps
sudo snap connect inference:hardware-observe  # GPU/NPU detection
sudo snap connect inference:system-observe

# from a local build (nothing auto-connects on a --dangerous install)
sudo snap install ./inference_0.1.0_amd64.snap --dangerous
sudo snap connect inference:snapd-control
sudo snap connect inference:hardware-observe
sudo snap connect inference:system-observe
sudo snap restart inference.proxy             # pick up socket access
snap connections inference
```

### 2. First run

```bash
inference            # hardware + profile + getting-started status
inference hardware   # detailed detection report
inference doctor     # drivers, backends, proxy health + fix-it commands
```

### 3. Catalogue, install & remove

```bash
inference catalogue                      # all models + which accelerate on your box
inference catalogue gemma                # typo-tolerant search
inference install gemma4                 # auto: silicon-optimal build
inference install gemma4+cuda            # force a CUDA build
inference install qwen-vl+npu+webui      # NPU variant + Open WebUI addon
inference install gemma4 --dry-run       # show the plan, change nothing
inference remove gemma4                  # uninstall
```

A model snap you `snap install` directly is **auto-discovered** within ~20 s — no need
to go through `inference install`.

### 4. The proxy (auto-started, runs as root)

```bash
snap services inference                       # see inference.proxy running
curl http://localhost:8080/v1/models          # federated model list
sudo snap restart inference.proxy             # after connecting interfaces
```

The federated proxy is the `inference.proxy` service — there is **no `serve` verb**.

### 5. Use it

```bash
inference chat                                # REPL: /model /system /models /quit
inference run gemma4 "explain RAID 5 briefly"
echo "summarize this" | inference run gemma4  # stdin/pipe
```

Any OpenAI client works unchanged:
```bash
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gemma4","messages":[{"role":"user","content":"hi"}]}'
```

### 6. External providers & aliases (litellm-style)

```bash
inference proxy add anthropic --key "$ANTHROPIC_API_KEY" --type anthropic --models claude-opus-4-8
inference proxy add openai    --key "$OPENAI_API_KEY"
inference config set alias.smart claude-opus-4-8     # then use model "smart"
```

### 7. Configure, update, remove

```bash
inference config get                     # full config
inference config set proxy.port 9000     # or: sudo snap set inference proxy.port=9000

sudo snap refresh inference              # update
sudo snap revert inference               # roll back
sudo snap remove inference               # remove (--purge to drop data)
```

---

### Notes
- The `inference.proxy` daemon runs as root and starts automatically on install.
- Provider API keys are stored in the snap's data dir and are never echoed back.
- `--json` and `--quiet` are available on every command for scripting / cloud-init.
