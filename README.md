# hash-plugins

Language-agnostic protocol-v1 SDK, examples, and separately built Hash smart
plugins. Nothing here installs or enables itself. The exact wire contract is
the Hash [plugin developer guide](https://github.com/tfcace/hash/blob/master/docs/plugins/README.md).

```sh
hash plugin install github:roeyazroel/hash-plugins --id io.runhash.autocorrection
hash plugin inspect io.runhash.autocorrection
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
```

The repository is a catalog of independently versioned plugins. Choose exactly
one plugin with `--id`, or install every catalog entry with `--all`; bare
install intentionally refuses to guess.

```sh
hash plugin install github:roeyazroel/hash-plugins --id io.runhash.adaptive-prediction
hash plugin enable io.runhash.adaptive-prediction
hash plugin doctor io.runhash.adaptive-prediction

hash plugin install github:roeyazroel/hash-plugins --id io.runhash.autosuggestions
hash plugin enable io.runhash.autosuggestions
hash plugin doctor io.runhash.autosuggestions
```

To install every plugin, then explicitly choose which ones may run:

```sh
hash plugin install github:roeyazroel/hash-plugins --all
hash plugin enable io.runhash.autocorrection
hash plugin enable io.runhash.adaptive-prediction
hash plugin enable io.runhash.autosuggestions
hash plugin doctor
```

Installation downloads the matching Darwin/Linux amd64/arm64 release archive.
It does not clone this repository or require Go. The plugin stays disabled until
the explicit `enable` command. To update or remove the managed bundle:

```sh
hash plugin upgrade io.runhash.autocorrection
hash plugin upgrade --all
hash plugin disable io.runhash.autocorrection
hash plugin uninstall io.runhash.autocorrection
```

`upgrade --all` upgrades Hash-managed plugins in deterministic ID order,
preserves their enabled state, and leaves developer-linked plugins untouched.
Every plugin release has its own tag (for example, `autosuggestions-v1.0.0`) and
publishes only that plugin's archives, a schema-v2 `HASH_PLUGINS.json`, and a
checksum file. The catalog resolves the selected plugin to its release tag.
Catalog snapshots have their own tags (for example, `catalog-v1.0.0`) and
publish the signed catalog plus its checksum file as the latest release.

Developers can still build and link a checkout; see
[plugins/autocorrection/README.md](plugins/autocorrection/README.md).
Adaptive prediction details are in
[plugins/adaptive-prediction/README.md](plugins/adaptive-prediction/README.md).
Warp-style literal-prefix history details are in
[plugins/autosuggestions/README.md](plugins/autosuggestions/README.md).

The Go and Python examples are offline deterministic all-hook examples. They
show all lifecycle/editor/command responses and all host-service message
shapes; see their README for build, invocation, transcript, disable and cleanup.
