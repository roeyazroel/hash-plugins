#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: package-plugin.sh <plugin-id> <version> <output-dir>" >&2
  exit 2
fi

plugin_id=$1
plugin_version=$2
output_dir=$3
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

case "$plugin_id" in
  io.runhash.autocorrection)
    plugin_dir=autocorrection
    binary=hash-autocorrection
    release_tag="autocorrection-v$plugin_version"
    ;;
  io.runhash.adaptive-prediction)
    plugin_dir=adaptive-prediction
    binary=hash-adaptive-prediction
    release_tag="adaptive-prediction-v$plugin_version"
    ;;
  io.runhash.autosuggestions)
    plugin_dir=autosuggestions
    binary=hash-autosuggestions
    release_tag="autosuggestions-v$plugin_version"
    ;;
  *)
    echo "unknown shipped plugin: $plugin_id" >&2
    exit 2
    ;;
esac

case "$plugin_version" in
  *[!0-9.]* | *..* | .* | *.)
    echo "plugin version must be numeric semver: $plugin_version" >&2
    exit 2
    ;;
esac

cd "$repo_dir"
python3 - "$plugin_id" "$plugin_version" "$release_tag" "$binary" <<'PY'
import json
import re
import sys

plugin_id, version, release_tag, binary = sys.argv[1:]
if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version):
    raise SystemExit("plugin version must be MAJOR.MINOR.PATCH")
with open("HASH_PLUGINS.json", encoding="utf-8") as source:
    index = json.load(source)
entry = index.get("plugins", {}).get(plugin_id)
if index.get("schema_version") != 2 or not entry:
    raise SystemExit("HASH_PLUGINS.json must contain the plugin in schema version 2")
if entry.get("version") != version or entry.get("release_tag") != release_tag:
    raise SystemExit("catalog version or release_tag does not match package request")
for platform in ("darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"):
    artifact = entry.get("artifacts", {}).get(platform, {}).get("name", "")
    expected = f"{binary}_{version}_{platform.replace('/', '_')}.tar.gz"
    if artifact != expected:
        raise SystemExit(f"catalog artifact for {platform} = {artifact!r}, want {expected!r}")
PY

if ! grep -Fqx "version = \"$plugin_version\"" "plugins/$plugin_dir/hash-plugin.toml"; then
  echo "manifest version does not match package request" >&2
  exit 1
fi

mkdir -p "$output_dir"
cp HASH_PLUGINS.json "$output_dir/HASH_PLUGINS.json"
stage_dir=$(mktemp -d)
trap 'rm -rf "$stage_dir"' EXIT HUP INT TERM

for target in "darwin amd64" "darwin arm64" "linux amd64" "linux arm64"; do
  set -- $target
  os=$1
  arch=$2
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -o "$stage_dir/$binary" "./plugins/$plugin_dir"
  archive="$output_dir/${binary}_${plugin_version}_${os}_${arch}.tar.gz"
  python3 scripts/package-archive.py "$stage_dir/$binary" "plugins/$plugin_dir/hash-plugin.toml" "$archive"
done
