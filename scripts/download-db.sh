#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
data_dir="$project_dir/data"
mode=${1:-download}
if [ "$#" -gt 1 ] || { [ "$mode" != download ] && [ "$mode" != --update ]; }; then
    echo "Usage: $0 [--update]" >&2
    exit 2
fi

if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$@"; }
else
    sha256() { shasum -a 256 "$@"; }
fi

if [ "$mode" = download ] && (cd "$data_dir" && sha256 -c SHA256SUMS) >/dev/null 2>&1; then
    echo "Databases already match the pinned SHA-256 checksums."
    exit 0
fi

if [ "$mode" = --update ]; then
    ref=$(git ls-remote https://github.com/lionsoul2014/ip2region.git HEAD | awk 'NR == 1 {print $1}')
else
    ref=$(cat "$data_dir/upstream-ref")
fi
case "$ref" in
    *[!0-9a-f]*|'') echo "Invalid upstream commit" >&2; exit 1 ;;
esac
[ "${#ref}" -eq 40 ] || { echo "Expected a complete upstream commit" >&2; exit 1; }

temporary_dir=$(mktemp -d "$data_dir/.download.XXXXXX")
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM
for file in ip2region_v4.xdb ip2region_v6.xdb; do
    echo "Downloading $file at $ref..."
    curl --fail --silent --show-error --location --retry 3 --connect-timeout 10 --max-time 600 \
        "https://raw.githubusercontent.com/lionsoul2014/ip2region/$ref/data/$file" \
        --output "$temporary_dir/$file"
done

if [ "$mode" = --update ]; then
    (cd "$temporary_dir" && sha256 ip2region_v4.xdb ip2region_v6.xdb) > "$temporary_dir/SHA256SUMS"
else
    cp "$data_dir/SHA256SUMS" "$temporary_dir/SHA256SUMS"
fi
(cd "$temporary_dir" && sha256 -c SHA256SUMS)
for file in ip2region_v4.xdb ip2region_v6.xdb; do
    chmod 644 "$temporary_dir/$file"
    mv "$temporary_dir/$file" "$data_dir/$file"
done
if [ "$mode" = --update ]; then
    printf '%s\n' "$ref" > "$temporary_dir/upstream-ref"
    mv "$temporary_dir/SHA256SUMS" "$data_dir/SHA256SUMS"
    mv "$temporary_dir/upstream-ref" "$data_dir/upstream-ref"
fi
echo "Databases ready at upstream commit $ref. Restart the service to load them."
