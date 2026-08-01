# Offline all-hooks examples

These examples are disabled until you explicitly link and enable one. They
never use network access. Build and run the Go version:

```sh
git clone https://github.com/roeyazroel/hash-plugins.git
cd hash-plugins
mkdir -p examples/go-all-hooks/bin examples/python-all-hooks/bin
go build -o examples/go-all-hooks/bin/all-hooks ./examples/go-all-hooks
cp examples/python-all-hooks/all_hooks.py examples/python-all-hooks/bin/all-hooks
chmod +x examples/python-all-hooks/bin/all-hooks
hash plugin link "$PWD/examples/go-all-hooks"
hash plugin enable io.runhash.examples.go-all-hooks
hash plugin doctor
# In a fresh Hash terminal: type `git`, observe ` status`; Right accepts it.
hash plugin disable io.runhash.examples.go-all-hooks
rm "$XDG_DATA_HOME/hash/plugins/io.runhash.examples.go-all-hooks"
```

Repeat with `python-all-hooks` to exercise the Python implementation. Both
handlers cover session lifecycle, cwd, prompt, suggestion, completion,
before/after command, history, command execution, and demonstrate the shapes
used by every host service in the Hash
[plugin developer guide](https://github.com/tfcace/hash/blob/master/docs/plugins/README.md).
