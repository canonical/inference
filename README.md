# inference

`inference` discovers inference-snap providers and reports their installed
status from snapd.

A provider is an OpenAI-compatible inference backend. An inference snap is the
currently supported provider type and packaging mechanism. The snap catalog
contains installable inference snaps; it is not a catalog of every possible
provider type.

```console
inference providers [--format=table|json] [--installed]
```

The default table has `PROVIDER`, `TYPE`, and `STATUS` columns. JSON output is
an object with a `providers` array. `--installed` excludes providers whose
status is `not installed`.

Inference-snap provider identities are read from the public
[`Onboarded Snaps`](https://canonical.github.io/inference-snaps-admin/onboarded-snaps.html)
table. The resolved snap catalog is cached for one hour at
`$SNAP_USER_COMMON/snap-catalog.json` inside the snap, or in the platform user
cache under `inference/snap-catalog.json`. Failed refreshes use a stale cache,
then the packaged seed; warnings are written to stderr. The snap build fetches
the snap catalog and installs this seed at
`share/inference/snap-catalog-seed.json`.

The strict snap requires the `network` and `snapd-control` interfaces. A local
development install may require:

```console
sudo snap connect inference:snapd-control
```
