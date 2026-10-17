#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/bord-terminal-smoke.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

echo "terminal smoke host: ${host_os}/${host_arch}"
go test ./internal/client ./internal/tui ./cmd/bord

host_binary="$tmp/bord"
if [[ "$host_os" == windows ]]; then
  host_binary="$host_binary.exe"
fi
go build -o "$host_binary" ./cmd/bord
"$host_binary" version

for target_os in linux darwin windows; do
  suffix=
  if [[ "$target_os" == windows ]]; then
    suffix=.exe
  fi
  echo "cross smoke: ${target_os}/amd64"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go build -o "$tmp/bord-${target_os}-amd64${suffix}" ./cmd/bord
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/client-${target_os}-amd64.test${suffix}" ./internal/client
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/tui-${target_os}-amd64.test${suffix}" ./internal/tui
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/bord-${target_os}-amd64.test${suffix}" ./cmd/bord
done

echo "terminal smoke matrix passed"
