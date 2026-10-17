#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output=${1:-"$repo_root/docs/performance/mvp-profile.json"}
duration=${BORD_LOAD_DURATION:-10s}

case "$output" in
  /*) ;;
  *) output="$repo_root/$output" ;;
esac

mkdir -p "$(dirname -- "$output")"
cd "$repo_root"
BORD_LOAD_PROFILE=1 \
BORD_LOAD_DURATION="$duration" \
BORD_LOAD_COMMIT="$(git rev-parse HEAD)" \
BORD_LOAD_OUT="$output" \
go test ./internal/integration -run '^TestLoadProfile$' -count=1 -v

printf 'load profile written to %s\n' "$output"
