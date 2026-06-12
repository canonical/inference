# `inference` — Implementation TODO

Derived from [DESIGN.md](DESIGN.md). Checked items are delivered in the first pass
(verified working on an Intel Lunar Lake laptop federating the `qwen-vl` inference snap).

## Milestone 0 — First pass (laptop) ✅ target of this commit
- [x] Project scaffold: Go module, `cmd/inference`, `internal/*` packages, `snapcraft.yaml`
- [x] **Hardware detection** — CPU/ISA, RAM, disk, GPU (NVIDIA/AMD/Intel), NPU (`/dev/accel*`)
- [x] **Profiler** — auto-classify edge / laptop / workstation / server
- [x] **`inference hardware`** — detection report
- [x] **Backend discovery** — known inference model snaps via `snap get -d`, config providers
- [x] **Default model** — wizard recommends **gemma4** as the starting model
- [x] **Proxy / router (litellm-lite)** — one OpenAI-compatible endpoint, name+alias routing,
      per-backend base-path translation (e.g. qwen-vl OpenVINO `/v3`), streaming passthrough, auto-refresh
- [x] **Federated proxy daemon** — auto-started `inference.proxy` service (root broker);
      `serve` is its internal entrypoint, not a user-facing verb
- [x] **`inference models` / `list`** — backends, models, aliases, online state
- [x] **`inference run <model> [prompt]`** — one-shot (stdin/pipe, `--json`)
- [x] **`inference chat`** — interactive REPL with `/model`, `/system`, `/models`, `/clear`, `/quit` (streaming)
- [x] **`+` grammar parser** — base + engine + accelerator + quant + addons / component-only
- [x] **`inference install <+spec>`** — resolve → plan → confirm (hybrid: drive snap or note engine path)
- [x] **`inference doctor`** — hardware + driver advice + proxy/backends + fix-it commands
- [x] **`inference catalogue`** — browse models + per-machine fit + install status
- [x] **`inference remove <model>`** — uninstall (daemon-brokered, streamed progress)
- [x] **Dynamic discovery** — any installed snap serving an OpenAI endpoint is federated,
      not just the hardcoded catalogue
- [x] **Typo tolerance** — fuzzy `catalogue <query>` + did-you-mean on install/remove
- [x] **`inference proxy add <provider> --key`** — register an external provider
      (OpenAI-compatible works today; Anthropic registers but doesn't route until Milestone R Phase 2)
- [x] **`inference config get/set`**, **`inference` wizard/status**, global flags `--json/--quiet/--yes/--dry-run`

## Milestone 1 — Make it a real installed snap
- [x] Build snap (`snapcraft pack --use-lxd`) → `inference_0.1.0_amd64.snap`
- [x] **Strict confinement** + interfaces: network, network-bind, hardware-observe,
      system-observe, **snapd-control** (manage/read model snaps via snapd REST API)
- [x] `snapd` client: snapd-socket REST API (root daemon) / `snap` CLI in dev
- [x] **Root-daemon broker**: snapd authorizes management by uid, so the auto-started
      root `inference.proxy` does discovery + installs; the unprivileged CLI delegates
      via `/v1/backends` and `/v1/install`
- [x] Bundle `pciutils` + fix disk detection; GPU/NPU detection verified inside sandbox
- [x] **No restart per install**: daemon refreshes discovery immediately after a brokered
      install + re-scans every 20 s, so model installs never need a proxy restart. The only
      restart is one-time on a `--dangerous` sideload after manual `snap connect snapd-control`
      (store installs auto-connect; repeat sideloads keep connections)
- [x] **Config source-of-truth**: single store owned by the root daemon
      (`$SNAP_DATA/config.json`); the unprivileged CLI delegates reads/writes via
      `GET`/`POST /v1/config` (`config get/set`, `proxy add`). API keys stored
      root-side, redacted (`***`) on read — never echoed. Daemon re-discovers on
      change so new providers/aliases route immediately.
      (Future: also mirror into `snap set inference` for ecosystem consistency.)
- [x] **`doctor --fix`**: adds a "Snap interfaces" section (reads real connection state
      via `snapd.Connections()`), flags unconnected privileged plugs + missing drivers,
      and `--fix` prints one ordered copy-paste remediation block (sudo steps the confined
      CLI can't run itself)
- [ ] Publish to a store channel (external release step — not a code task)

## Milestone 2 — `+` grammar & resolver depth
- [x] **`--profile <p>` global flag**: overrides auto-detected profile and drives resolver
      defaults (edge → aggressive quant + cpu/npu preference)
- [x] **Resolver depth**: real per-base accelerator coverage; impossible requests
      (e.g. `nemotron-3-nano+rocm`, `+cuda` with no NVIDIA GPU) error with the valid
      alternatives instead of silently falling back
- [ ] Hybrid engine path: install standalone engine (llama.cpp/vllm) + pull weights when no snap exists
- [ ] Addons: `+webui` (Open WebUI), `+api` (Anthropic-format shim), `+bench` — currently
      advisory-only in the plan; auto-install + proxy wiring not yet implemented
- [ ] Driver advisor `--fix` actually runs apt/driver installs after confirmation

## Milestone R — Remote providers & aggregators ([DESIGN](DESIGN.md) §4.1)

Federate hosted models (OpenAI, Anthropic, Google) and OpenAI-compatible
aggregators (OpenRouter, Together.ai, Groq, Fireworks, Azure-OpenAI, self-hosted
vLLM) behind the same `:8080/v1`. Foundations exist from M0/M1 — `proxy add`, the
config broker, and remote-backend discovery — but `handleProxy` only speaks
OpenAI Bearer passthrough, so `proxy add anthropic` currently **stores** a
provider it can't actually route to. Architecture rule: a new OpenAI-compatible
vendor/aggregator = **one preset row, zero code**; a non-compatible API = **one
`Adapter`**.

### Phase 1 — Passthrough presets (OpenAI · Google · OpenRouter · Together)
- [ ] **Preset table** (name → `Type`, `BaseURL`, `AuthHeader`, `Extra`, `Models`,
      `Discover`) for `openai`, `google`, `openrouter`, `together`
- [ ] `proxy add <preset> --key …` fills base/auth/version/models from the table;
      `--base`/`--type openai` override for any generic OpenAI-compatible vendor/gateway/self-host
- [ ] **`config.Provider.Extra map[string]string`** (version/ranking-header overrides);
      brokered via `POST /v1/config`, redacted on read like the API key
- [ ] Per-request header injection from preset+`Extra` (Bearer vs `x-api-key`;
      OpenRouter `HTTP-Referer`/`X-Title`)
- [ ] **Google** via its OpenAI-compat base (`…/v1beta/openai/`), Bearer
- [ ] Verify each with a real key: `curl :8080/v1/chat/completions` returns an
      OpenAI-shaped result; `stream:true` yields chunks

### Phase 2 — Native adapter layer (Anthropic)
- [ ] **`internal/provider` package** + `Adapter` interface
      (`BuildRequest`/`WriteResponse`); `provider.For(type)` registry defaulting to passthrough
- [ ] Refactor **`handleProxy`** to route via `provider.For(b.Type)`
      (passthrough = the current byte-copy, extracted into `Passthrough{}`)
- [ ] **`Anthropic{}` request map**: `POST {base}/messages`; `x-api-key` +
      `anthropic-version`; hoist system → top-level `system`; inject `max_tokens`
      default; `stop`→`stop_sequences`; carry `temperature`/`top_p`
- [ ] **Anthropic response map**: `content[].text`→`message.content`;
      `stop_reason`→`finish_reason`; `input`/`output_tokens`→`prompt`/`completion_tokens`;
      synthesize `id`/`object`/`created`
- [ ] **Anthropic streaming**: SSE (`message_start`/`content_block_delta`/
      `message_delta`/`message_stop`) → OpenAI `chat.completion.chunk` + `data: [DONE]`
- [ ] **Fixture-based unit tests** (recorded request/response + a multi-event stream), no network
- [ ] (later) tools/function-calling + vision/image content-block mapping

### Phase 3 — Discovery, addressing & telemetry
- [ ] **`--discover`** opt-in live model fetch (OpenAI `GET /models`, Anthropic
      `GET /v1/models`, aggregator `GET /models`), cached per backend; aggregators
      default `Discover=true`, direct providers ship a small curated highlight list
- [ ] **`backend:model` selector** (colon, since aggregator ids contain `/`) for
      collisions + slashed ids; alias support; update `backend.FindForModel`
- [ ] **`inference models` summarizes** large aggregator catalogs
      (`openrouter — 312 models, use --all`) + filter
- [ ] **`inference usage`** (tokens/latency/$) via a static, overridable price
      table — uniform `usage.*` after translation (also a Milestone 3 item)
- [ ] **Failure-mode polish**: surface provider 401/403 verbatim + `→ proxy add … --key`
      hint; missing key → backend `offline`; named 502 on provider down

## Milestone 3 — Router features
- [ ] Fallback chains (`route set X --fallback Y`), load-balancing across identical backends
- [ ] Lifecycle: `keep-warm` / `lazy` (idle-unload) / `pinned` per model
- [ ] `--on-oom spill-to-remote` policy; `inference usage` (tokens/latency/cost)
- [ ] `/v1/embeddings` routing; "did you mean" on unknown model 404

## Milestone 4 — Scale-out & polish
- [ ] `inference serve --bind 0.0.0.0`, `cluster join`, coordinator load-balancing by free VRAM
- [ ] `inference benchmark <model>` (tok/s), first-token-latency estimates in plans
- [ ] cloud-init / Juju / MicroK8s integration notes
- [ ] Cold-start transcript tests per profile; `+` grammar coverage matrix tests; off-TTY contract tests

## Milestone V2 — Shared local runtime & unified backends ([DESIGN](DESIGN.md) §11)

Forward-looking and **purely additive**: model snaps stay packaged-everything and
usable standalone. Every model becomes a proxied backend in one of three kinds —
`remote` (§4.1), `local-proxied` (the snap's own server), `local-hosted`
(`inference`'s shared runtime). `local-hosted` only kicks in when a snap shares
weights and a matching engine component is present; otherwise we fall back to
`local-proxied`, so the system is useful at every step.

### Phase A — Unify local snaps as "local providers" (mostly exists)
- [ ] Reframe `backend.Discover` snap backends as `local-proxied` providers sharing
      the §4.1 provider/adapter path (OVMS `/v3` = a passthrough provider with base-path)
- [ ] Add a `Kind` column to `inference models`: `remote` / `local-proxied` / `local-hosted`
- [ ] De-risk spike: **content-interface fan-in** prototype (many model-snap slots →
      one `inference` consumer) — the #1 unknown; `local-proxied` needs none of it

### Phase B — Shared runtime foundation (engines)
- [ ] Engines as snap **components** (`llamacpp-{cpu,cuda,rocm,sycl}`, `openvino`,
      `vllm-cuda`); root daemon installs the silicon-matched component on demand
- [ ] `+engine`/`+accel` resolve to a component (extend `internal/install` resolver +
      `spec` grammar); reuse hard-error-with-alternatives
- [ ] **Engine supervisor** in the daemon: launch/reuse an engine process on a private
      loopback port, proxy `/v1/*` to it (router base-path translation already exists)
- [ ] GPU userspace via Canonical content snaps (`graphics-core22` / `gpu-2404`);
      device access plugs (`/dev/dri`, `/dev/kfd`, `/dev/nvidia*`, `/dev/accel*`)

### Phase C — Weight sharing → `local-hosted`
- [ ] **Model `manifest.json`** schema (formats[], params, context, modalities, RAM floor)
- [ ] Model snaps expose a read-only `content: inference-model` weights slot (additive,
      non-breaking); `inference` plugs + mounts it
- [ ] **format → engine → accelerator** resolver (gguf→llama.cpp, openvino-ir→OpenVINO,
      safetensors→vLLM) over manifest × hardware × installed components × `+` override
- [ ] Discovery yields hostable models from manifests; prefer `local-hosted` when weights
      + engine component available, else `local-proxied` (graceful fallback)

### Phase D — Hosting policy, lifecycle, telemetry
- [ ] Per-model **hosting policy**: prefer shared-runtime vs the snap's own tuned server
- [ ] Lifecycle on hosted models: `lazy` (idle-unload) / `keep-warm` / `pinned`,
      VRAM-aware admission, `--on-oom spill-to-remote` (ties to Milestone 3)
- [ ] Reference: publish one weights-slot model snap (gguf `gemma4`) + verify a model
      runs on the shared runtime end-to-end on real silicon

### Open decisions (DESIGN §11.11)
- [ ] Engine packaging: components (recommended) vs separate engine snaps vs fat bundle
- [ ] Weight sharing: content interface (preferred) vs shared on-disk cache vs proxied-only
- [ ] vLLM scope: first cut vs deferred behind llama.cpp + OpenVINO
