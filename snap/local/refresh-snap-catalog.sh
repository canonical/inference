#!/bin/sh
set -eu

url="https://canonical.github.io/inference-snaps-admin/onboarded-snaps.json"
dest="$SNAP_COMMON/onboarded-snaps.json"
tmp="$dest.tmp"

curl -fsSL -o "$tmp" "$url"
mv -f "$tmp" "$dest"
