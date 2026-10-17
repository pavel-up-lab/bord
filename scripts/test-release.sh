#!/bin/sh
set -eu

version="${1:-v0.0.0-test}"
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-1700000000}"
python3 scripts/release.py --version "$version" --output dist-test
(cd dist-test && sha256sum --check SHA256SUMS)
rm -rf .release-smoke
mkdir .release-smoke
tar -xzf "dist-test/bord_${version}_linux_amd64.tar.gz" -C .release-smoke
tar -xzf "dist-test/bord-server_${version}_linux_amd64.tar.gz" -C .release-smoke
".release-smoke/bord_${version}_linux_amd64/bord" --version | grep -F "$version"
".release-smoke/bord-server_${version}_linux_amd64/bord-server" version | grep -F "$version"
rm -rf .release-smoke dist-test
