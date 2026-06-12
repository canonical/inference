# `inference` — Orchestration Layer UX Design Spec

## Context

Today the inference-snaps ecosystem ships six model snaps (`deepseek-r1`, `gemma3`,
`gemma4`, `nemotron-3-nano`, `nemotron-3-nano-omni`, `qwen-vl`). Each one bundles an
engine, runs its own hardware detection at install, and exposes an OpenAI-compatible
endpoint. This is great per-model, but it pushes three hard problems onto the user:

1. **Variant roulette** — acceleration coverage is uneven (AMD ROCm only lands on
   `gemma3`; Intel NPU only on `deepseek-r1`/`qwen-vl`). Users can't tell what will
   actually accelerate on their box without trial and error.
2. **Driver yak-shaving** — getting CUDA/ROCm/NPU runtimes in place is manual and
   error-prone, and failures surface as silent CPU fallback.
3. **N endpoints, no glue** — every model is its own port with its own lifecycle, and
   there's no single API to talk to all of them (or to external providers).

`inference` is a new orchestration snap installed with `sudo snap install inference`.
It is the **front door** to local AI on Ubuntu: it detects hardware, advises/installs
drivers, resolves the best engine build per model, federates every endpoint behind one
litellm-style OpenAI-compatible proxy, and gives a delightful interactive CLI. Goal:
make local inference *one command and silicon-optimal* on every Ubuntu machine — edge
IoT, laptops, workstations, and cluster nodes — with the same muscle memory.

**Locked design decisions:**
- Architecture is **hybrid**: if a model snap exists, drive it; otherwise install an
  engine + weights directly. Both fronted by one proxy.
- **Generic from day 1**: auto-detect a machine *profile* (edge/laptop/workstation/
  server) and tune defaults; no single hero persona.
- **`+` composes both** the runtime stack (engine/accelerator) **and** addons
  (UIs/tools/APIs): `gemma3+cuda+webui`.

---

## Design principles

1. **Zero-config, never zero-control.** A bare `inference` gets a working chat in under
   a minute; every automatic choice is inspectable and overridable.
2. **Honest about silicon.** Always show what is accelerated and what fell back to CPU —
   no silent degradation.
3. **Plan, then act.** Every mutating command prints a resolved plan (snaps, drivers,
   sudo, disk) and asks once. `--dry-run` and `--yes` everywhere; `--json` for automation.
4. **One mental model across scales.** The same verbs work on a Pi, a laptop, and a
   cluster node — only the profile-tuned defaults differ.
5. **Reuse, don't reinvent.** Drive the existing model snaps and their engine managers
   where they exist; only install standalone engines to fill gaps.

---

## Implementation architecture (v0.1, as built)

A Go snap (single static binary), **strictly confined**, plugging `network`,
`network-bind`, `hardware-observe`, `system-observe`, and **`snapd-control`**.

The defining constraint discovered during bring-up: **snapd authorizes snap
management by uid, not by interface.** A confined, non-root process cannot read
another snap's config or install/remove snaps over either snapd socket
(`/run/snapd.socket` → 401 by uid; `/run/snapd-snap.socket` → 403). So:

- **`inference.proxy` is a root daemon** (auto-started on install) — the single
  privileged broker. It discovers model snaps (reads their config via the snapd
  REST API as root, authorized because `snapd-control` grants the AppArmor access
  and uid 0 satisfies snapd) and installs/removes them.
- **The CLI is unprivileged and delegates** to the daemon over `localhost`:
  `inference models/doctor/catalogue` read `GET /v1/backends`; `install`/`remove`
  stream progress from `POST /v1/install` · `/v1/remove`; `config get/set` and
  `proxy add` read/write `GET` · `POST /v1/config`. The daemon owns the only
  writable config store (`$SNAP_DATA`, root-only), so the CLI never writes it
  directly; provider API keys are stored root-side and redacted on read.
- **Discovery is dynamic**: every installed snap that serves an OpenAI endpoint
  (`/v3` for OpenVINO Model Server, `/v1` for llama.cpp/standard — probed) is
  federated, so a plain `snap install <model>` is picked up automatically; the
  curated [catalogue] adds metadata + per-machine fit and is still shown offline.
- **The proxy** federates all backends behind one OpenAI-compatible endpoint
  (`:8080/v1`), routing by model id / alias / backend name, translating each
  backend's base path, and streaming responses through.
- **Daemon lifecycle — no restart per install.** Installing a model
  (`inference install <model>`, or a plain `snap install`) never requires
  restarting the proxy: the daemon refreshes discovery immediately after a
  brokered install and re-scans every 20 s, so new backends federate on their
  own. The **only** restart is a one-time step on a sideloaded (`--dangerous`)
  build, where nothing auto-connects: after the manual
  `snap connect inference:snapd-control` the daemon is restarted once so it comes
  up privileged. On a store install (auto-connections) and on repeat
  `--dangerous` reinstalls of the same snap (connections persist), no manual
  restart is needed at all.

- **Resolver depth (M2)**: the `+` resolver uses real per-base accelerator
  coverage — an impossible request (`gemma4+rocm`, or `+cuda` on a box with no
  NVIDIA GPU) is a hard error that names the valid alternative
  (`try gemma4+cpu`) instead of silently falling back. `--profile <p>` overrides
  the detected profile and tunes accelerator preference (edge → npu/cpu first)
  and default quant. `doctor --fix` reads live interface state and emits one
  ordered remediation block. (Addons like `+webui` are still advisory-only in
  the plan — auto-install + proxy wiring is not yet implemented.)

Component packages: `hardware` (detect+profile), `snapd` (REST/CLI broker),
`backend` (discovery), `catalogue` (curated models + fuzzy match), `spec` (`+`
grammar), `install` (resolve→plan), `proxy` (router + management API + client),
`chat` (run/REPL), `cli`.

---

## 1. First-run experience (the wow moment)

A bare `inference` with nothing set up launches a guided wizard; once set up it shows
status. The wizard is the single most important surface — it must feel like the machine
is introducing itself.

```
$ inference
  Welcome to Inference 👋  Let's get your machine ready for local AI.

  Detecting hardware…
    CPU    AMD Ryzen 9 7950X · 32 threads · AVX-512            ✓
    GPU    NVIDIA RTX 4090 · 24 GB VRAM                        ✓ driver 550 · CUDA 12.4
    NPU    none                                                –
    RAM    64 GB        Disk  412 GB free
    Profile:  workstation   (single high-VRAM GPU)

  Recommended starting point
    gemma4   text+vision   →  llama.cpp (cuda)   ~2.5 GB   fits comfortably in VRAM
    (gemma4 is the default starting model; pick another with [m])

  This will:
    + install gemma4 (cuda, amd64)            snap     2.5 GB
    + start the inference proxy on :8080      service
    → OpenAI-compatible API at  http://localhost:8080/v1

  [Enter] to set up · [m] pick a different model · [a] advanced · [q] quit
```

On completion it drops straight into chat so the very first session ends in a working
conversation, then prints the one line developers need:

```
  ✔ Ready.  Proxy live at http://localhost:8080/v1  (model: gemma3)
  Try it:   inference chat        or      curl localhost:8080/v1/models
```

Profile detection (auto, generic from day 1) tunes the recommendation:
- **edge** (≤4 GB RAM / no GPU / ARM SBC): smallest aggressively-quantized model, CPU/NPU,
  low context, headless, no wizard prompts under `--quiet`.
- **laptop** (single GPU/iGPU/NPU, battery): mid quant, power-aware concurrency.
- **workstation** (one big GPU): higher quant, vision models on by default.
- **server** (multi-GPU / headless / many cores): bind `0.0.0.0`, higher concurrency,
  no interactive prompts unless a TTY is attached.

---

## 2. The `+` grammar (signature interaction)

`+` composes a **stack spec**: a base, optional runtime variants, and optional addons.
The resolver classifies each token by dimension and fills the rest from hardware.

```
inference install <base>[+<token>]...        e.g.  gemma3+vllm+cuda+webui
inference install +<component>               component-only (driver/engine/tool)
inference install <base-a> <base-b>+npu      multiple bases in one shot
```

Token dimensions (resolver knows which is which; order-independent):

| Dimension      | Examples                                  | If omitted |
|----------------|-------------------------------------------|------------|
| base (model)   | `gemma3`, `qwen-vl`, `llama-3.1-8b`       | required (unless component-only) |
| engine         | `llama.cpp`, `vllm`, `openvino`           | best for base+hardware |
| accelerator    | `cuda`, `rocm`, `vulkan`, `npu`, `cpu`    | best detected, drivers permitting |
| quant          | `q4`, `q8`, `fp16`                        | profile default |
| addon          | `webui`, `openai-api`, `api`, `bench`     | none |
| component-only | `nvidia-drivers`, `rocm`, `cuda-toolkit`  | n/a |

Examples and what they mean:

```
inference install gemma3                 # auto everything for this box
inference install gemma3+cuda            # force CUDA build (error if no usable GPU)
inference install gemma3+rocm+q4         # ROCm engine, q4 weights
inference install qwen-vl+npu+webui      # NPU variant + Open WebUI addon
inference install llama-3.1-8b+vllm      # not a snap → hybrid path: engine+weights
inference install +nvidia-drivers        # component-only: just fix drivers
```

**Hybrid resolution rule:**
```
base has a model snap with a variant matching the resolved accelerator?
    → drive the existing snap (snap install <base>, set engine via its config)
else
    → install the requested/auto engine snap + pull weights, manage it directly
both outcomes register a backend with the proxy
```

`+` works symmetrically for `remove`: `inference remove gemma3+webui` drops just the addon.

---

## 3. Install / resolution flow

Every install resolves to an explicit, confirm-once plan. The plan is where driver
advice, sudo, and "this won't accelerate" warnings surface — never mid-download.

```
$ inference install gemma3+rocm
  Resolving gemma3+rocm for your system…

  ⚠ AMD GPU detected (RX 7900 XTX) but ROCm runtime not installed.
    Required:  rocm 6.x  (apt, ~4 GB, needs sudo)

  Plan
    + rocm 6.x                         apt    ~4.0 GB   sudo
    + gemma3 (rocm, amd64)             snap    2.3 GB
    + connect gemma3:opengl,kernel     snap interfaces
    → engine: llama.cpp (rocm build)
    → register backend → proxy :8080  as 'gemma3'

  Disk after:  398 GB free        Est. first-token: ~0.4 s

  Proceed? [Y/n/--dry-run already shown]
```

Failure modes are first-class and actionable:
- Asked for `+cuda` with no NVIDIA GPU → hard error naming what *would* work
  (`try gemma3+vulkan or gemma3+cpu`).
- Variant unavailable for base (e.g. `nemotron-3-nano+rocm` not built) → explain and
  offer the hybrid engine path or the nearest accelerated option.
- Drivers present but snap interface unconnected → the plan includes the `snap connect`.

---

## 4. The proxy / router (litellm-like)

`inference` runs one snap service: an OpenAI-compatible gateway on `:8080/v1` that
federates every local backend, plus external providers and remote nodes. One endpoint,
many models, name-based routing, fallbacks, aliases — application code is identical to
calling `api.openai.com`.

```
$ inference models
  LOCAL
    gemma3            llama.cpp/cuda     ● running    :11434   24 tok/s
    qwen-vl           openvino/npu       ○ stopped
  REMOTE
    claude-opus-4-8   anthropic          ● configured
    gpt-x             openai             ● configured
  ALIASES
    fast   → gemma3
    smart  → claude-opus-4-8
    auto   → cheapest-capable (router policy)
```

Managing routes mirrors litellm ergonomics:

```
inference proxy add anthropic --key $ANTHROPIC_API_KEY     # keys stored in snap secrets
inference proxy add openai    --key $OPENAI_API_KEY
inference alias set smart claude-opus-4-8
inference route set gemma3 --fallback smart                # local-first, spill to cloud
inference proxy policy --on-oom spill-to-remote            # VRAM pressure → remote
```

Routing features:
- **Name or alias** in the `model` field; unknown names 404 with a "did you mean".
- **Fallback chains** (local → remote) and **load-balancing** across identical backends
  (multiple GPUs, multiple cluster nodes).
- **Lifecycle/lazy-load**: models can be `keep-warm`, `lazy` (load on first request,
  unload after idle), or `pinned` — crucial on laptops/edge with tight VRAM.
- **Cost/telemetry**: `inference usage` shows tokens, latency, and $ for remote calls.

Default endpoint is OpenAI-compatible (`/v1/chat/completions`, `/v1/models`,
`/v1/embeddings`); an optional `+api` addon can expose an Anthropic-format shim.

---

## 5. Interactive chat REPL

```
$ inference chat
  gemma3 · llama.cpp/cuda · 24 GB VRAM · /help for commands
  › explain attention like I'm five
  Attention is how the model decides which words to pay attention to…

  › /model qwen-vl          # hot-swap model (lazy-loads, warns on VRAM)
  › /image ~/cat.png what breed?
  › /system you are a terse senior SRE
  › /save chat.md  · /tokens · /speed · /quit
```

- `inference run gemma3 "one-shot prompt"` for scripting/pipes; reads stdin, honors
  `--json`. `cat err.log | inference run smart "summarize"`.
- REPL slash commands: `/model`, `/image`, `/system`, `/temp`, `/tokens`, `/speed`,
  `/save`, `/clear`. Switching models routes through the same proxy, so cloud and local
  models are interchangeable mid-conversation.

---

## 6. Hardware & driver intelligence (the engine room)

Surfaced through `inference hardware` and `inference doctor`, reusing the detection
tooling the model snaps already rely on (`pciutils`, `nvidia-utils`, `clinfo`,
`rocminfo`), wrapped in friendly output and *fix-it* commands.

```
$ inference doctor
  Hardware
    NVIDIA RTX 4090            ✓ visible    driver 550   CUDA 12.4
    Intel NPU                  ⚠ present but no driver     → inference install +intel-npu
  Snap interfaces
    gemma3:opengl              ✗ not connected             → sudo snap connect gemma3:opengl
  Proxy
    :8080                      ✓ listening   3 backends    1 warm
  Disk                         ✓ 398 GB free
  1 issue can be auto-fixed.  Run:  inference doctor --fix
```

- **Detector** → CPU features, GPU vendor/VRAM/driver/compute-capability, NPU, RAM/disk.
- **Profiler** → classifies machine (edge/laptop/workstation/server) → default tuning.
- **Resolver** → (base, hardware, drivers, availability) → concrete snap or engine build.
- **Driver advisor** → detects missing/old runtimes, prints exact install commands, and
  with `--fix`/confirmation runs them. Never installs drivers silently.

---

## 7. Scale-out & edge (one model, every machine)

Because the proxy *is* the unit of federation, scale is just "more backends":

```
# server / cluster node
inference serve --bind 0.0.0.0:8080
inference cluster join coordinator.local:8080 --advertise gemma3,qwen-vl

# coordinator load-balances across joined nodes by model + free VRAM
inference cluster status
```

- A **coordinator** is just an `inference` proxy whose backends are remote nodes; the
  router's load-balancing and fallback logic is reused unchanged.
- **Edge**: `inference install --profile edge gemma3` forces aggressive quant + CPU/NPU,
  caps memory, runs headless; `--quiet` suppresses all prompts for image builds.
- Plays nicely with existing Ubuntu orchestration (cloud-init for unattended setup;
  Juju/MicroK8s charms can wrap `inference serve`) — noted, not built in v1.

---

## 8. Command surface (reference)

```
inference                       # wizard (first run) | status (configured)
inference catalogue [query]     # browse models + per-machine fit + status (typo-tolerant)
inference install <+spec>...    # resolve → plan → confirm → install/register
inference remove <model>...     # uninstall (typo-tolerant; daemon-brokered)
inference list | models         # installed backends + models + aliases
inference run <model> [prompt]  # one-shot (stdin/pipe, --json)
inference chat                  # interactive REPL
inference proxy <add|policy|…>  # external providers, routing policy
inference alias|route set …     # name routing, fallbacks
inference cluster <join|status> # scale-out
inference hardware              # detection report
inference doctor [--fix]        # diagnose + repair drivers/interfaces/ports
inference benchmark <model>     # tok/s on this silicon
inference usage                 # tokens/latency/cost
inference config <get|set>      # thin wrapper over `snap set inference …`
```

Global flags everywhere: `--dry-run`, `--yes`, `--json`, `--quiet`, `--profile <p>`.

> The federated proxy runs automatically as the **`inference.proxy`** systemd/snap
> service — there is no user-facing `serve` verb (it is the daemon's internal
> entrypoint). Manage it with `snap start|stop|restart inference.proxy`.

---

## 9. Config model

`inference config get/set` (and `proxy add`) edit a single source of truth held
by the **root daemon** in `$SNAP_DATA/config.json`. Because snapd authorizes
writes by uid, the unprivileged CLI cannot write that file directly; instead it
delegates to the daemon over `GET`/`POST /v1/config`, which validates the key,
persists it, and re-discovers backends so a new provider/alias routes
immediately. Valid keys today: `proxy.port`, `proxy.bind`, `alias.<name>`.
Provider API keys are stored root-side and **redacted (`***`) on every read** —
`config get` never echoes a secret. (Future: mirror into `snap set inference`
for ecosystem consistency; `config export/import` for reproducible/edge builds.)

---

## 10. Delight touches (the difference between "tool" and "loved tool")

- First run **always ends in a working chat**, not a success message.
- Plans show **estimated first-token latency and disk-after**, not just download size.
- Every warning carries the **exact next command** (`→ inference install +intel-npu`).
- `inference benchmark` lets users *see* their silicon win — shareable tok/s.
- Color + spinners on a TTY; clean `--json` / `--quiet` off-TTY (CI, cloud-init, edge).
- `--dry-run` on every mutating verb; the wizard's `[a] advanced` reveals full control
  without cluttering the happy path.

---

## How to validate this design before building

This is a design spec, so "tests" are usability and coverage walkthroughs:

1. **Cold-start walkthrough** per profile — script the exact terminal transcript for
   edge (Pi), laptop (iGPU/NPU), workstation (1 GPU), server (multi-GPU). Confirm a
   first working chat in ≤ 60 s with zero manual driver steps on the happy path.
2. **`+` grammar coverage matrix** — enumerate base × engine × accelerator and verify the
   resolver rule (existing-snap vs hybrid engine path) and the error/advice for every
   unsupported cell (e.g. `nemotron-3-nano+rocm`). Cross-check against the real variant
   matrix on the snaps reference page.
3. **Driver-advisor honesty** — for each missing-runtime case (CUDA/ROCm/Intel-NPU,
   unconnected snap interface), confirm `doctor`/install surfaces the exact fix command
   and never falls back to CPU silently.
4. **Proxy parity** — a `curl localhost:8080/v1/chat/completions` and an OpenAI-SDK
   snippet must be byte-identical to cloud usage; verify alias, fallback, and lazy-load
   transitions in the transcript.
5. **Off-TTY contract** — re-run each flow with `--quiet --json --yes` and confirm no
   prompts, machine-readable output, and clean exit codes (for cloud-init/CI/edge images).
6. **PM review gate** — walk the spec past 2–3 target users (a laptop dev, a cluster
   operator) for the "would this delight me" read before committing engineering effort.
