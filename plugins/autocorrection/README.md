# Hash Autocorrection

This plugin is stateless, local-only, network-free, telemetry-free, and disabled
until explicitly enabled. It corrects only a failed static executable,
subcommand, or long flag when bounded diagnostics and successful history or
core-local completion provide conservative evidence.

## Build, install, and configure

```sh
cd /Users/roeyazroel/Documents/github/roeyazroel/hash-plugins
go build -trimpath -o plugins/autocorrection/hash-autocorrection ./plugins/autocorrection
hash plugin link "$PWD/plugins/autocorrection"
hash plugin inspect io.runhash.autocorrection
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
```

Add optional settings to the Hash configuration:

```toml
[plugins.settings."io.runhash.autocorrection"]
strategies = ["executable", "subcommand", "long_flag"]
history_limit = 100
max_candidates = 3
```

Open a new interactive Hash session. A deterministic transcript after `git
status` already exists as successful history is:

```text
$ git sttaus
git: 'sttaus' is not a git command. See 'git --help'.
$ git status
      ^ dim correction ghost; press Right
$ git status
      ^ buffer is filled, but nothing executes
$ <Enter>
On branch main
```

For several equal candidates, Up/Down or Ctrl-P/Ctrl-N chooses one. Enter fills
and closes the chooser; a second Enter executes. Escape or typing dismisses.

## Disable, rollback, and clean up safely

```sh
hash plugin disable io.runhash.autocorrection
# Start a new Hash session to confirm it is inactive.
plugin_link="${XDG_DATA_HOME:-$HOME/.local/share}/hash/plugins/io.runhash.autocorrection"
test -L "$plugin_link"
rm "$plugin_link"
rm -f plugins/autocorrection/hash-autocorrection
```

Inspect the exact path printed by `hash plugin link` before removing it. The
`test -L` guard refuses to remove a real directory. Never remove the whole
plugins directory. Re-linking the prior bundle version is the rollback path.

The plugin writes no raw command, diagnostic, history, output, or environment
value to stderr. `hash plugin doctor io.runhash.autocorrection` is the first
troubleshooting step for executable, protocol-version, handshake, or shutdown
failures.
