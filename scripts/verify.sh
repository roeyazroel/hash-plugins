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
test -f plugins/autocorrection/hash-plugin.toml
test -f plugins/adaptive-prediction/hash-plugin.toml
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
  cp HASH_PLUGINS.json dist/HASH_PLUGINS.json
  (cd dist && shasum -a 256 -c SHA256SUMS)
fi
