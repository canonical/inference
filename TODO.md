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
- [x] **`inference proxy add <provider> --key`** — register external OpenAI/Anthropic provider
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
- [ ] `snap connect` advice surfaced by `doctor --fix`; publish to a store channel

## Milestone 2 — `+` grammar & resolver depth
- [ ] Full variant matrix per base (real coverage from snaps reference page)
- [ ] Hybrid engine path: install standalone engine (llama.cpp/vllm) + pull weights when no snap exists
- [ ] Addons: `+webui` (Open WebUI), `+api` (Anthropic-format shim), `+bench`
- [ ] `--profile edge` aggressive quant + memory caps; `remove <+spec>` symmetry
- [ ] Driver advisor `--fix` actually runs apt/driver installs after confirmation

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
