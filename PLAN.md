# Inference CLI implementation plan

## Purpose

Build the `inference` application from a clean branch, starting with:

```text
inference providers [--format=table|json] [--installed]
```

The implementation should establish maintainable patterns for later commands
without importing the proof-of-concept architecture wholesale.

The `inference-snaps-cli` repository is the primary reference for Go, Cobra,
command construction, output formatting, dependency injection, testing, and
Snapcraft conventions. The `inference` `dev` branch is a behavioral reference,
especially for communicating with snapd, but its code should not be copied
without reconsidering its architecture and error handling.

## Agreed scope

This first slice supports inference snaps only.

Configured OpenAI-compatible providers such as Ollama or remote OpenAI
endpoints are out of scope. The provider domain model must nevertheless allow
additional provider types and sources to be added later without changing the
public shape of `inference providers`.

No install, remove, proxy, daemon, tray, hardware detection, or provider
configuration behavior is included in this slice.

## Command contract

### Usage

```text
inference providers [--format=table|json] [--installed]
```

The command accepts no positional arguments.

Flags:

- `--format=table|json` selects the output format and defaults to `table`.
- `--installed` excludes providers whose status is `not installed`.

Unsupported formats and unexpected positional arguments must fail before any
network or snapd work is performed.

### Table output

The table has exactly these columns:

```text
PROVIDER  TYPE  STATUS
```

Rows are sorted lexically by provider name. The provider type for this slice is
always `inference-snap`.

For an installed provider, `STATUS` is the status string reported by snapd,
without translating it to an inference-specific status. For example, if snapd
reports `active` or `installed`, the command prints `active` or `installed`.
This supersedes the earlier proposal to map snapd statuses to `enabled` and
`disabled`.

When a catalog provider does not appear in snapd's installed-snap response, its
status is:

```text
not installed
```

The installation hint is table-only and is shown when the displayed result
contains at least one not-installed provider:

```text
Hint: run "inference install <provider>" to install providers.
```

It is not shown for JSON, for an empty result, or for `--installed`, because
that filter removes all not-installed rows.

### JSON output

JSON uses a top-level object containing a `providers` array:

```json
{
  "providers": [
    {
      "provider": "gemma4",
      "type": "inference-snap",
      "status": "active"
    }
  ]
}
```

An empty result is encoded as:

```json
{
  "providers": []
}
```

Warnings and diagnostics go to stderr. Stdout must remain valid JSON whenever
JSON output succeeds.

## Provider identity and public catalog

### Catalog source

The primary catalog is the publicly accessible generated JSON:

```text
https://raw.githubusercontent.com/canonical/inference-snaps-admin/refs/heads/main/github-projects/onboarded-snaps.generated.json
```

Its current shape is:

```json
{
  "repositories": {
    "catalog-key": {
      "full_name": "canonical/example-snap",
      "html_url": "https://github.com/canonical/example-snap",
      "model_name": "Example",
      "visibility": "public"
    }
  }
}
```

Only entries whose `visibility` is exactly `public` are providers.

The catalog object key, `model_name`, repository basename, and punctuation
normalization are not authoritative provider identifiers.

### Authoritative snap name

For every public catalog repository, fetch this file from that repository's
default branch:

```text
snap/snapcraft.yaml
```

The root-level `name` field in that file is the authoritative provider and snap
name. This avoids incorrect assumptions for repositories such as:

| Repository | Snap name |
| :--- | :--- |
| `canonical/glm-4.7-flash-snap` | `glm-4-7-flash` |
| `canonical/nomic-embed-text-v1.5-snap` | `nomic-embed-text-v1-5` |
| `canonical/qwen-vl-snap` | `qwen-vl` |
| `canonical/qwen3.5-snap` | `qwen3-5` |
| `canonical/qwen3.6-snap` | `qwen3-6` |
| `canonical/qwen3.8-snap` | `qwen3-8` |

Use GitHub's public repository contents API to resolve
`snap/snapcraft.yaml` from the repository's default branch. Construct API
requests from a validated `full_name` under the fixed `api.github.com` host;
do not follow arbitrary catalog URLs. Decode and parse the returned file
content locally.

The resolver must:

- require a non-empty literal root `name`;
- validate the value as a snap name;
- reject duplicate snap names;
- reject malformed, private, empty, or incomplete catalog snapshots;
- apply bounded response sizes and HTTP timeouts;
- use a descriptive `User-Agent`;
- use bounded concurrency for repository lookups; and
- publish a refreshed catalog only after every entry has resolved
  successfully.

A partial refresh must not silently remove providers. If any repository cannot
be resolved, the whole candidate refresh is discarded and fallback behavior is
used.

### Confirmed current public catalog

The current public JSON contains these 15 repositories and declared snap names:

| Repository | Snap name |
| :--- | :--- |
| `canonical/deepseek-r1-snap` | `deepseek-r1` |
| `canonical/gemma3-snap` | `gemma3` |
| `canonical/gemma4-snap` | `gemma4` |
| `canonical/glm-4.7-flash-snap` | `glm-4-7-flash` |
| `canonical/nemotron-3.5-lightning-snap` | `nemotron-3-5-lightning` |
| `canonical/nemotron-3-nano-snap` | `nemotron-3-nano` |
| `canonical/nemotron-3-nano-omni-snap` | `nemotron-3-nano-omni` |
| `canonical/nomic-embed-text-v1.5-snap` | `nomic-embed-text-v1-5` |
| `canonical/qwen-vl-snap` | `qwen-vl` |
| `canonical/qwen3-snap` | `qwen3` |
| `canonical/qwen3.5-snap` | `qwen3-5` |
| `canonical/qwen3.6-snap` | `qwen3-6` |
| `canonical/qwen3.8-snap` | `qwen3-8` |
| `canonical/qwen3-coder-snap` | `qwen3-coder` |
| `canonical/smollm2-snap` | `smollm2` |

This table records the current observation only. Runtime behavior must continue
to resolve the live public catalog rather than hard-code this list.

## Catalog cache and offline behavior

Use a resolved catalog snapshot as the cache unit. A resolved snapshot contains
the validated repository identity and authoritative snap name, so a cache hit
does not issue one request per repository.

Cache locations:

- inside the snap: `$SNAP_USER_COMMON/catalog.json`;
- outside the snap: the platform user-cache directory under
  `inference/catalog.json`.

The cache envelope is versioned and records at least:

- schema version;
- successful fetch timestamp; and
- resolved providers.

The cache time-to-live is one hour.

Resolution order:

1. Use a valid cache no older than one hour.
2. Otherwise fetch and fully resolve the public catalog.
3. If refresh fails, use a valid stale cache and warn on stderr.
4. If no valid cache exists, use an embedded build-time seed and warn on
   stderr.

Malformed caches are never treated as successful data. Report a warning and
continue through the normal refresh and seed fallback path.

A successfully resolved remote catalog remains usable if writing the cache
fails, but the cache-write failure is reported on stderr.

Cache replacement must be atomic:

1. serialize and validate the complete replacement;
2. write a uniquely named temporary file in the destination directory;
3. close the file successfully; and
4. rename it over the prior cache.

Never truncate a last-known-good cache before its replacement is ready.

The embedded seed is a committed, canonical resolved snapshot. A small Go
generator updates it through the same parser and validator used at runtime.
Snap builds may use network access to refresh the seed before compiling the
binary.

## snapd integration

### API behavior

Use snapd's HTTP API over a Unix socket and query:

```text
GET /v2/snaps
```

Do not add `select=all`, because inactive historical revisions must not produce
duplicate or misleading provider rows.

Decode the snapd response envelope and retain, at minimum, each current snap's:

- `name`;
- `status`.

For every catalog provider:

- matching snapd entry: preserve the exact non-empty snapd `status`;
- no matching snapd entry: use `not installed`.

Installed snaps that are not in the public provider catalog are ignored.

If snapd cannot be reached, returns an API error, returns malformed data, or
omits the status of a matching provider, fail the command. Do not report every
provider as not installed and do not return partial output.

### Socket selection

Host development and confined execution use the same typed client and transport
abstraction. The implementation never shells out to `snap`.

Candidate sockets:

- host execution: `/run/snapd.socket`;
- confined execution: `/run/snapd-snap.socket`, with
  `/run/snapd.socket` considered only where the connected interface and snapd
  authorization permit it.

Use a custom `http.Transport` with Unix `DialContext`, a short connection
timeout, a bounded request timeout, bounded response reads, and contextual
errors.

The list endpoint is read-only, so this slice should not introduce the root
daemon used by the `dev` proof of concept for privileged operations. Confined
socket behavior must be confirmed with a snap smoke test. If target snapd
versions reject the read-only request, revisit the privilege boundary rather
than silently adding a shell or sudo fallback.

## Proposed package structure

This repository builds a self-contained application rather than a reusable Go
library. Following the official Go module-layout guidance, supporting packages
belong under `internal` so other modules cannot accidentally depend on
implementation APIs. The `pkg` directory has no special meaning to the Go
toolchain and should be reserved for a deliberate convention around public,
supported packages. If reusable APIs are needed later, expose only those
specific packages at the module root or move them into a dedicated module;
do not turn all application internals into public packages.

```text
cmd/inference/
  main.go                 process entry point and dependency construction

internal/command/
  context.go              injected services and stdout/stderr
  root.go                 Cobra root command
  providers.go            flags, validation, rendering, and hint policy

internal/providers/
  provider.go             provider domain types
  service.go              catalog/snapd join, filtering, and ordering

internal/catalog/
  types.go                remote and resolved catalog types
  parser.go               public JSON and snapcraft YAML parsing
  github.go               bounded public GitHub client
  cache.go                versioned atomic cache
  resolver.go             TTL and fallback policy
  seed.json               embedded resolved catalog
  generate.go             go:generate declaration
  cmd/update-seed/        seed updater

internal/snapd/
  client.go               typed snapd client
  transport.go            Unix-socket HTTP transport
  types.go                minimal response DTOs

snap/
  snapcraft.yaml
```

Dependency direction:

```text
command -> providers service <- catalog and snapd adapters
```

The provider service depends on narrow interfaces rather than concrete HTTP or
filesystem implementations. Infrastructure packages return typed results,
warnings, and errors; they do not print.

The provider status should remain a string because installed statuses are owned
by snapd. `not installed` is the only status synthesized by the provider service.
Provider type can be a named string type with `inference-snap` as its first
defined value.

## CLI construction and output

Use Cobra, following `inference-snaps-cli` patterns:

- command factory functions;
- a command-specific struct for flags and dependencies;
- `cobra.NoArgs`;
- `cobra.NoFileCompletions`;
- `SilenceUsage: true`;
- explicit format validation;
- injected stdout and stderr; and
- errors returned to the root rather than printed deep in packages.

Use the table library and borderless formatting pattern already used by
`inference-snaps-cli`. Keep rendering separate from provider discovery so exact
output can be tested without network or snapd.

Do not use package-global output flags or direct `fmt.Print` calls.

## Snap packaging

Create a strict `core24` snap:

```text
name: inference
```

The default app is also named `inference` and executes the Go CLI.

Required plugs for this slice:

- `network`, for the public catalog and repository manifest requests;
- `snapd-control`, for access to snapd.

Use the Go Snapcraft plugin and the Go toolchain version used by the reference
CLI. Follow its version-adoption pattern where practical.

The super-privileged `snapd-control` interface requires store review and may
require an explicit connection in development installations. Document that
requirement and produce a clear snapd error when the interface is disconnected.

## Failure and warning policy

Fatal errors:

- invalid command arguments or output format;
- snapd unavailable or returning untrustworthy data;
- no usable remote, cached, or embedded catalog;
- rendering or stdout write failure.

Warnings with successful output:

- stale cache used after refresh failure;
- embedded seed used because no valid cache is available;
- malformed cache ignored;
- refreshed data used but cache persistence failed.

Warnings go to stderr and must identify the degraded source without dumping
large HTTP bodies or internal data.

Remote and cache errors must be wrapped with enough context to identify the
operation and source. Avoid broad catches, silent fallbacks, and success-shaped
empty results.

## Implementation sequence

1. **Scaffold the module and snap**
   - Add `go.mod`, the process entry point, Cobra root command, and minimal
     `snap/snapcraft.yaml`.
   - Add only dependencies required by this slice.

2. **Define the provider domain**
   - Add provider type, status, catalog source, and installed-snap interfaces.
   - Implement deterministic joining, `--installed` filtering, and sorting.

3. **Implement snapd**
   - Add the Unix-socket transport and typed `/v2/snaps` client.
   - Preserve exact snapd status values and explicit errors.

4. **Implement catalog parsing and resolution**
   - Parse the generated public JSON.
   - Resolve each public repository's authoritative snap name from
     `snap/snapcraft.yaml`.
   - Validate the complete snapshot.

5. **Implement caching and the embedded seed**
   - Add the one-hour TTL resolver, atomic cache, fallbacks, and warnings.
   - Add reproducible seed-update tooling and commit the initial resolved seed.

6. **Implement `providers` output**
   - Wire Cobra flags to the provider service.
   - Add exact table, JSON, hint, stdout, and stderr behavior.

7. **Document and package**
   - Document command usage, JSON schema, cache behavior, network access,
     `snapd-control`, and seed updates.
   - Build and exercise the strict snap in a disposable environment.

## Test plan

### Catalog tests

- Public entries are included and non-public entries are excluded.
- Catalog map order does not affect output.
- Repository `snapcraft.yaml` `name` is used instead of the catalog key,
  repository basename, or `model_name`.
- Dotted repository names resolve to their declared hyphenated snap names.
- Missing file, missing root `name`, invalid YAML, invalid snap name, duplicate
  names, and duplicate repositories fail candidate resolution.
- Empty and partially resolved remote catalogs are rejected.
- HTTP timeout, non-200 response, API error, invalid base64, malformed JSON,
  malformed YAML, and oversized responses are handled explicitly.
- Repository lookups stay on the fixed GitHub API host.

### Cache tests

- Fresh cache is used without HTTP requests.
- Exactly one-hour-old and stale-cache boundaries are deterministic through an
  injected clock.
- Successful refresh atomically replaces the cache.
- Failed or partial refresh preserves the old cache.
- Stale cache and embedded seed fallbacks return the expected warning.
- Malformed and future-dated caches are rejected.
- Cache-write failure does not discard successfully refreshed runtime data.

### snapd tests

- Host and confined socket selection.
- Real HTTP exchange through a temporary Unix socket.
- Successful synchronous snap-list envelope decoding.
- Exact preservation of statuses such as `active` and `installed`.
- API error envelope, non-200 response, malformed JSON, missing result,
  duplicate current records, unavailable socket, and timeout.
- The request does not use `select=all`.

### Provider service tests

- Installed catalog provider receives snapd's exact status.
- Absent catalog provider receives `not installed`.
- Unknown snapd status strings are preserved.
- `--installed` includes every installed status and excludes only
  `not installed`.
- Non-catalog installed snaps are ignored.
- Results are sorted by authoritative snap name.
- Snapd failure never becomes an all-not-installed success.

### Command tests

- Exact table headers, spacing, rows, ordering, blank line, and hint.
- Exact JSON object and field names.
- Empty JSON uses `[]`, not `null`.
- `--installed` table and JSON behavior.
- JSON stdout stays valid when warnings are written to stderr.
- Invalid format and positional arguments do not call dependencies.
- Dependency errors produce no partial stdout.
- All rendering uses injected writers.

### Snap smoke tests

- Build and install the strict snap in a disposable VM.
- Connect `snapd-control`.
- Verify `inference providers` can read `/v2/snaps` while confined.
- Compare displayed installed statuses with the raw snapd response.
- Disconnect `snapd-control` and verify an explicit failure.
- Verify JSON with a standard JSON parser.
- Block network access and verify stale-cache and embedded-seed behavior.

## Acceptance criteria

- The default snap app runs as `inference`.
- The CLI uses Cobra and follows `inference-snaps-cli` structural patterns.
- Every valid public catalog repository is resolved through its own
  `snap/snapcraft.yaml`.
- Provider names exactly match the snaps' declared names.
- Installed provider statuses exactly match snapd's status strings.
- Absent providers are reported as `not installed`.
- `--installed` and both output formats match their contracts.
- Catalog outages degrade through stale cache and embedded seed without
  corrupting machine-readable output.
- snapd failures never fabricate provider statuses.
- Host and confined execution share one direct snapd client abstraction and
  never shell out to `snap`.
- Unit, command, Unix-socket integration, and snap smoke tests cover the
  behavior above.

## Decisions recorded

- Use Go and Cobra.
- Use `inference-snaps-cli` as the primary style reference.
- Implement inference-snap providers only in the first slice.
- Keep the provider model extensible for future OpenAI-compatible providers.
- Fetch the public catalog at runtime.
- Use publicly accessible JSON rather than parsing the HTML page.
- Cache a last-known-good resolved catalog.
- Refresh after one hour.
- Fall back to an embedded build-time seed on first-run network failure.
- Use a top-level `providers` object for JSON.
- Use actual snap names as provider identifiers.
- Read actual snap names from each repository's `snap/snapcraft.yaml`.
- Preserve the status reported by snapd rather than mapping it.
- Synthesize only the absent status, `not installed`.
- Use snapd HTTP over Unix sockets both inside and outside the snap.
- Never shell out to the `snap` CLI.
- Package the application as the strict `inference` snap with a default
  `inference` app and `snapd-control`.
