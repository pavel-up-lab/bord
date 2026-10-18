#!/bin/sh
set -eu

version="${1:-v0.0.0-test}"

if python3 scripts/release.py --version 'v../../unsafe' --output dist-invalid >/dev/null 2>&1; then
	echo "unsafe release version was accepted" >&2
	exit 1
fi
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD)}"
first=dist-test-first
second=dist-test-second
smoke=.release-smoke

cleanup() {
	if [ "${KEEP_RELEASE_ARTIFACTS:-0}" != 1 ]; then
		python3 - "$first" "$second" "$smoke" <<'PY'
import pathlib
import shutil
import sys
for value in sys.argv[1:]:
    path = pathlib.Path(value)
    if path.is_dir():
        shutil.rmtree(path)
PY
	fi
}
trap cleanup EXIT INT TERM

python3 scripts/release.py --version "$version" --output "$first"
python3 scripts/release.py --version "$version" --output "$second"
(cd "$first" && sha256sum --check SHA256SUMS)
(cd "$second" && sha256sum --check SHA256SUMS)
cmp "$first/SHA256SUMS" "$second/SHA256SUMS"

python3 scripts/validate-release.py --directory "$first" --version "$version" --extract "$smoke"
"$smoke/bord_${version}_linux_amd64/bord" --version | grep -F "$version"
"$smoke/bord-server_${version}_linux_amd64/bord-server" version | grep -F "$version"
printf 'release validation passed: reproducible archives, seven targets, native smoke\n'
