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

hash_repo=${HASH_REPO:-"$repo_dir/../hash"}
if [ -f "$hash_repo/go.mod" ]; then
  (cd "$hash_repo" && go test ./internal/plugin -run 'Schema|ProtocolSchema' -count=1)
fi

if command -v goreleaser >/dev/null 2>&1; then
  goreleaser release --snapshot --clean
  archive=$(find dist -name 'hash-autocorrection_*_darwin_arm64.tar.gz' -print -quit)
  tar -tzf "$archive" | grep -qx 'hash-autocorrection'
  tar -tzf "$archive" | grep -qx 'hash-plugin.toml'
  python3 -c '
import json
with open("HASH_PLUGINS.json", encoding="utf-8") as source:
    index = json.load(source)
artifacts = index["plugins"]["io.runhash.autosuggestions"]["artifacts"]
expected = {
    "darwin/amd64": "hash-autosuggestions_{{version}}_darwin_amd64.tar.gz",
    "darwin/arm64": "hash-autosuggestions_{{version}}_darwin_arm64.tar.gz",
    "linux/amd64": "hash-autosuggestions_{{version}}_linux_amd64.tar.gz",
    "linux/arm64": "hash-autosuggestions_{{version}}_linux_arm64.tar.gz",
}
assert {key: value["name"] for key, value in artifacts.items()} == expected
'
  for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
    set -- dist/hash-autosuggestions_*_"$platform".tar.gz
    test "$#" -eq 1
    autosuggestions_archive=$1
    test -f "$autosuggestions_archive"
    tar -tzf "$autosuggestions_archive" | grep -qx 'hash-autosuggestions'
    tar -tzf "$autosuggestions_archive" | grep -qx 'hash-plugin.toml'
  done
  cp HASH_PLUGINS.json dist/HASH_PLUGINS.json
  (cd dist && shasum -a 256 -c SHA256SUMS)
fi
