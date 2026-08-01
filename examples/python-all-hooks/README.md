# Python protocol-v1 all-hooks example

This single-file deterministic example requires no package install or network
access and is never enabled automatically. Current Hash operates only the
correction slice; the other handlers demonstrate reserved wire shapes.

```sh
git clone https://github.com/roeyazroel/hash-plugins.git
cd hash-plugins
mkdir -p examples/python-all-hooks/bin
cp examples/python-all-hooks/all_hooks.py examples/python-all-hooks/bin/all-hooks
chmod +x examples/python-all-hooks/bin/all-hooks
python3 -m py_compile examples/python-all-hooks/all_hooks.py
hash plugin link "$PWD/examples/python-all-hooks"
hash plugin inspect io.runhash.examples.python-all-hooks
hash plugin enable io.runhash.examples.python-all-hooks
hash plugin doctor io.runhash.examples.python-all-hooks
```

Expected correction transcript:

```text
$ git sttaus
git: 'sttaus' is not a git command
$ git status          # Right fills the dim ghost without execution
$ <Enter>             # the next Enter executes
```

Disable and remove only explicit generated artifacts:

```sh
hash plugin disable io.runhash.examples.python-all-hooks
plugin_link="${XDG_DATA_HOME:-$HOME/.local/share}/hash/plugins/io.runhash.examples.python-all-hooks"
test -L "$plugin_link" && rm "$plugin_link"
rm -f examples/python-all-hooks/bin/all-hooks
```

The source shows handlers for every hook category. Reserved host-service
request shapes are in the Hash developer guide; `host.environment.get` and
`host.output.write` remain unavailable in this release.
