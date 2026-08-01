# hash-plugins

Language-agnostic protocol-v1 SDK, examples, and separately built Hash smart
plugins. Nothing here installs or enables itself. The exact wire contract is
the Hash [plugin developer guide](https://github.com/tfcace/hash/blob/master/docs/plugins/README.md).

```sh
hash plugin install github:roeyazroel/hash-plugins
hash plugin inspect io.runhash.autocorrection
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
```

The release contains both shipped plugins. Select adaptive prediction
explicitly when installing a multi-plugin release:

```sh
hash plugin install github:roeyazroel/hash-plugins --id io.runhash.adaptive-prediction
hash plugin enable io.runhash.adaptive-prediction
hash plugin doctor io.runhash.adaptive-prediction
```

Installation downloads the matching Darwin/Linux amd64/arm64 release archive.
It does not clone this repository or require Go. The plugin stays disabled until
the explicit `enable` command. To update or remove the managed bundle:

```sh
hash plugin upgrade io.runhash.autocorrection
hash plugin disable io.runhash.autocorrection
hash plugin uninstall io.runhash.autocorrection
```

Developers can still build and link a checkout; see
[plugins/autocorrection/README.md](plugins/autocorrection/README.md).
Adaptive prediction details are in
[plugins/adaptive-prediction/README.md](plugins/adaptive-prediction/README.md).

The Go and Python examples are offline deterministic all-hook examples. They
show all lifecycle/editor/command responses and all host-service message
shapes; see their README for build, invocation, transcript, disable and cleanup.
