# Hash Autocorrection

This plugin is stateless, local-only, makes no network requests, emits no
telemetry, and is disabled until explicitly enabled. It corrects only a failed
static executable, subcommand, or long flag when bounded diagnostics provide
the failed token and at least one conservative evidence source provides a
nearby replacement.

Evidence is command-agnostic. The plugin consumes explicit diagnostic
alternatives such as “did you mean,” “most similar command,” and “a similar
option exists,” plus successful history, core-local completion, and a bounded
snapshot of executable names from the inherited `PATH`. When a command's own
diagnostic explicitly prescribes `<same-command> --help`, the plugin may run
that exact executable with only the prescribed help flag and parse bounded
command sections. It never invokes a shell, another executable, or arbitrary
diagnostic arguments. Evidence queries run concurrently within Hash's hook
deadline. A diagnostic-provided alternative is preferred because it comes from
the command that rejected the token; otherwise independent evidence agreement
wins. Hash still validates that the result safely changes exactly one eligible
token.

## Install and configure

Install the prebuilt bundle from a GitHub Release. No source checkout or Go
toolchain is required:

```sh
hash plugin install github:roeyazroel/hash-plugins --id io.runhash.autocorrection
hash plugin inspect io.runhash.autocorrection
hash plugin enable io.runhash.autocorrection
hash plugin doctor io.runhash.autocorrection
```

For a reproducible install, append this plugin's release tag such as
`@autocorrection-v1.0.0`. The
installer selects the current OS and architecture, verifies the release
checksum, and leaves the plugin disabled by default.

Developers can instead build and link a checkout:

```sh
git clone https://github.com/roeyazroel/hash-plugins.git
cd hash-plugins
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

Open a new interactive Hash session. A diagnostic-provided alternative works
without a prior successful history entry:

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

## Upgrade, disable, and uninstall safely

```sh
hash plugin upgrade io.runhash.autocorrection
hash plugin disable io.runhash.autocorrection
hash plugin uninstall io.runhash.autocorrection
```

Upgrade atomically switches to the newly verified version and retains the old
managed version for rollback. Restart Hash after an upgrade so the interactive
session starts the new process. Uninstall disables the plugin and removes only
the bundle managed by `hash plugin install`; it refuses developer links.

The plugin writes no raw command, diagnostic, history, output, or environment
value to stderr. `hash plugin doctor io.runhash.autocorrection` is the first
troubleshooting step for executable, protocol-version, handshake, or shutdown
failures.
