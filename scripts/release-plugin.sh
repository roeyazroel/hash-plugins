#!/bin/sh
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: release-plugin.sh <release-tag> [output-dir]" >&2
  exit 2
fi

release_tag=$1
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${2:-"$repo_dir/dist"}

case "$release_tag" in
  autocorrection-v*)
    plugin_id=io.runhash.autocorrection
    version=${release_tag#autocorrection-v}
    ;;
  adaptive-prediction-v*)
    plugin_id=io.runhash.adaptive-prediction
    version=${release_tag#adaptive-prediction-v}
    ;;
  autosuggestions-v*)
    plugin_id=io.runhash.autosuggestions
    version=${release_tag#autosuggestions-v}
    ;;
  *)
    echo "release tag must name one shipped plugin" >&2
    exit 2
    ;;
esac

if [ -e "$output_dir" ] && [ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
  echo "output directory must not already contain files: $output_dir" >&2
  exit 2
fi
mkdir -p "$output_dir"
"$repo_dir/scripts/package-plugin.sh" "$plugin_id" "$version" "$output_dir"
(cd "$output_dir" && shasum -a 256 HASH_PLUGINS.json *.tar.gz > SHA256SUMS)
