#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/bord-terminal-smoke.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

echo "terminal smoke host: ${host_os}/${host_arch}"
go test ./internal/client ./internal/clientapp ./internal/adminclient ./internal/terminalui ./internal/tui ./internal/admintui ./internal/adminapp ./cmd/bord ./cmd/bord-admin

host_binary="$tmp/bord"
if [[ "$host_os" == windows ]]; then
  host_binary="$host_binary.exe"
fi
go build -o "$host_binary" ./cmd/bord
"$host_binary" version
host_admin_binary="$tmp/bord-admin"
if [[ "$host_os" == windows ]]; then
  host_admin_binary="$host_admin_binary.exe"
fi
go build -o "$host_admin_binary" ./cmd/bord-admin
"$host_admin_binary" version

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
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go build -o "$tmp/bord-admin-${target_os}-amd64${suffix}" ./cmd/bord-admin
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/adminclient-${target_os}-amd64.test${suffix}" ./internal/adminclient
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/admintui-${target_os}-amd64.test${suffix}" ./internal/admintui
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/adminapp-${target_os}-amd64.test${suffix}" ./internal/adminapp
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/terminalui-${target_os}-amd64.test${suffix}" ./internal/terminalui
  CGO_ENABLED=0 GOOS="$target_os" GOARCH=amd64 \
    go test -c -o "$tmp/bord-admin-${target_os}-amd64.test${suffix}" ./cmd/bord-admin
done

echo "terminal smoke matrix passed"
