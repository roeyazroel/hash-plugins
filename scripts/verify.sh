#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM

go test ./... -count=1
go test -race ./... -count=1
go test ./internal/conformance -count=1
go vet ./...
PYTHONPYCACHEPREFIX="$build_dir/pycache" python3 -m py_compile examples/python-all-hooks/all_hooks.py

go build -trimpath -o "$build_dir/hash-autocorrection" ./plugins/autocorrection
go build -trimpath -o "$build_dir/hash-adaptive-prediction" ./plugins/adaptive-prediction
go build -trimpath -o "$build_dir/hash-autosuggestions" ./plugins/autosuggestions
go build -trimpath -o "$build_dir/go-all-hooks" ./examples/go-all-hooks

test -x "$build_dir/hash-autocorrection"
test -x "$build_dir/hash-adaptive-prediction"
test -x "$build_dir/hash-autosuggestions"
test -f plugins/autocorrection/hash-plugin.toml
test -f plugins/adaptive-prediction/hash-plugin.toml
test -f plugins/autosuggestions/hash-plugin.toml
test -f HASH_PLUGINS.json
python3 scripts/validate-catalog.py

hash_repo=${HASH_REPO:-"$repo_dir/../hash"}
if [ -f "$hash_repo/go.mod" ]; then
  (cd "$hash_repo" && go test ./internal/plugin -run 'Schema|ProtocolSchema' -count=1)
fi

plugin_specs=$(python3 - <<'PY'
import json

with open("HASH_PLUGINS.json", encoding="utf-8") as source:
    catalog = json.load(source)
for plugin_id in sorted(catalog["plugins"]):
    entry = catalog["plugins"][plugin_id]
    binary = entry["artifacts"]["darwin/arm64"]["name"].split("_", 1)[0]
    print(f"{plugin_id}\t{entry['version']}\t{entry['release_tag']}\t{binary}")
PY
)
while IFS='	' read -r plugin_id plugin_version release_tag binary; do
  release_dir="$build_dir/$release_tag"
  ./scripts/package-plugin.sh "$plugin_id" "$plugin_version" "$release_dir"
  (cd "$release_dir" && shasum -a 256 HASH_PLUGINS.json *.tar.gz > SHA256SUMS)
  test -f "$release_dir/HASH_PLUGINS.json"
  test -f "$release_dir/SHA256SUMS"
  (cd "$release_dir" && shasum -a 256 -c SHA256SUMS)
  for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
    set -- "$release_dir/${binary}_${plugin_version}_${platform}.tar.gz"
    test "$#" -eq 1
    test -f "$1"
    tar -tzf "$1" | grep -qx "$binary"
    tar -tzf "$1" | grep -qx 'hash-plugin.toml'
  done
done <<EOF
$plugin_specs
EOF

release_script_dir="$build_dir/release-script"
autosuggestions_release_tag=$(printf '%s\n' "$plugin_specs" | awk -F '\t' '$1 == "io.runhash.autosuggestions" { print $3 }')
./scripts/release-plugin.sh "$autosuggestions_release_tag" "$release_script_dir"
(cd "$release_script_dir" && shasum -a 256 -c SHA256SUMS)

catalog_script_dir="$build_dir/release-catalog"
./scripts/release-catalog.sh catalog-v1.0.0 "$catalog_script_dir"
test -f "$catalog_script_dir/HASH_PLUGINS.json"
test -f "$catalog_script_dir/SHA256SUMS"
test "$(find "$catalog_script_dir" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d ' ')" -eq 2
(cd "$catalog_script_dir" && shasum -a 256 -c SHA256SUMS)
