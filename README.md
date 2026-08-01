# hash-plugins

Language-agnostic protocol-v1 SDK, examples, and separately built Hash smart
plugins. Nothing here installs or enables itself. The exact wire contract is
the Hash [plugin developer guide](../hash/docs/plugins/README.md).

```sh
go test ./...
python3 -m py_compile examples/python-all-hooks/all_hooks.py
go build -o plugins/autocorrection/hash-autocorrection ./plugins/autocorrection
hash plugin link "$PWD/plugins/autocorrection"
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
hash plugin disable io.runhash.autocorrection
plugin_link="${XDG_DATA_HOME:-$HOME/.local/share}/hash/plugins/io.runhash.autocorrection"
test -L "$plugin_link" && rm "$plugin_link"
rm -f plugins/autocorrection/hash-autocorrection
```

The Go and Python examples are offline deterministic all-hook examples. They
show all lifecycle/editor/command responses and all host-service message
shapes; see their README for build, invocation, transcript, disable and cleanup.
