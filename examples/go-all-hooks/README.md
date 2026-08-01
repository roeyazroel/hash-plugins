# Go protocol-v1 all-hooks example

This deterministic, network-free example is never enabled automatically. It
implements every reserved hook shape so SDK authors can exercise framing; only
the correction slice documented by the current Hash guide is operational.

```sh
cd /Users/roeyazroel/Documents/github/roeyazroel/hash-plugins
mkdir -p examples/go-all-hooks/bin
go build -trimpath -o examples/go-all-hooks/bin/all-hooks ./examples/go-all-hooks
hash plugin link "$PWD/examples/go-all-hooks"
hash plugin inspect io.runhash.examples.go-all-hooks
hash plugin enable io.runhash.examples.go-all-hooks
hash plugin doctor io.runhash.examples.go-all-hooks
```

Expected correction transcript after a successful `git status` exists in
history:

```text
$ git sttaus
git: 'sttaus' is not a git command
$ git status          # dim ghost; Right fills only
$ <Enter>             # a separate Enter executes
```

Disable and clean up only the explicit link and generated binary:

```sh
hash plugin disable io.runhash.examples.go-all-hooks
plugin_link="${XDG_DATA_HOME:-$HOME/.local/share}/hash/plugins/io.runhash.examples.go-all-hooks"
test -L "$plugin_link" && rm "$plugin_link"
rm -f examples/go-all-hooks/bin/all-hooks
```

The handlers cover session start/stop, cwd changes, typed prompt segments,
suggestion, completion, before-command transform, post-command correction,
history observation, and a declared command result. The source also serves as
the minimal Go handler reference; unavailable hooks are protocol examples, not
a promise that current Hash invokes them.
