#!/bin/sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: release-catalog.sh <catalog-tag> [output-dir]" >&2
  exit 2
fi

catalog_tag=$1
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${2:-"$repo_dir/dist"}

case "$catalog_tag" in
  catalog-v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "catalog tag must have the form catalog-v<major>.<minor>.<patch>" >&2
    exit 2
    ;;
esac

version=${catalog_tag#catalog-v}
case "$version" in
  *[!0-9.]* | *..* | .* | *.)
    echo "catalog version must be numeric semver: $version" >&2
    exit 2
    ;;
esac
old_ifs=$IFS
IFS=.
set -- $version
IFS=$old_ifs
if [ "$#" -ne 3 ] || [ -z "$1" ] || [ -z "$2" ] || [ -z "$3" ]; then
  echo "catalog version must be MAJOR.MINOR.PATCH: $version" >&2
  exit 2
fi

if [ -e "$output_dir" ] && [ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
  echo "output directory must not already contain files: $output_dir" >&2
  exit 2
fi
mkdir -p "$output_dir"

python3 "$repo_dir/scripts/validate-catalog.py" "$repo_dir"

cp "$repo_dir/HASH_PLUGINS.json" "$output_dir/HASH_PLUGINS.json"
(cd "$output_dir" && shasum -a 256 HASH_PLUGINS.json > SHA256SUMS)
