# Hash Autosuggestions

This plugin offers Warp-style ghost text from recent successful command
history. After two characters, it checks the current directory first, then all
Hash history, then an optional one-time import of other shell histories. It is
local-only: it never invokes completion, agents, network services, VCS, or
commands.

## Install and enable

Install the release archive for the current platform, then explicitly enable
the plugin:

```sh
hash plugin install github:roeyazroel/hash-plugins --id io.runhash.autosuggestions
hash plugin inspect io.runhash.autosuggestions
hash plugin enable io.runhash.autosuggestions
hash plugin doctor io.runhash.autosuggestions
```

For a development checkout:

```sh
go build -trimpath -o /tmp/hash-autosuggestions ./plugins/autosuggestions
mkdir -p /tmp/hash-autosuggestions-bundle
cp /tmp/hash-autosuggestions plugins/autosuggestions/hash-plugin.toml /tmp/hash-autosuggestions-bundle/
hash plugin link /tmp/hash-autosuggestions-bundle
hash plugin enable io.runhash.autosuggestions
```

## Configure

External-shell learning is disabled by default:

```toml
[plugins.settings."io.runhash.autosuggestions"]
learn_from_other_shells = false
```

Change it to `true` in `~/.config/hash/config.toml` only when a one-time local
import is wanted:

```toml
[plugins]
enabled = ["io.runhash.autosuggestions"]

[plugins.settings."io.runhash.autosuggestions"]
learn_from_other_shells = true
shells = ["zsh", "bash", "fish"]
history_limit = 100

# Optional overrides must be absolute paths or begin with ~/.
[plugins.settings."io.runhash.autosuggestions".history_paths]
zsh = "~/.zsh_history"
bash = "~/.bash_history"
fish = "~/.local/share/fish/fish_history"
```

`history_limit` controls each live Hash history request and must be in `1..100`.
When external learning is enabled for the first time, each selected history is
read from a bounded tail of at most 32 MiB and 50,000 commands. Missing or
unreadable histories are silently skipped. The private cache is stored at
`$XDG_DATA_HOME/hash/plugin-data/io.runhash.autosuggestions/history.db` (or
`~/.local/share/hash/...` when `XDG_DATA_HOME` is unset) with directory mode
0700 and file mode 0600.

The import is deliberately one-time. To learn later commands from external
shells, disable the plugin, exit every Hash session, delete `history.db`, then
restart and enable the plugin:

```sh
hash plugin disable io.runhash.autosuggestions
cache_root=${XDG_DATA_HOME:-"${HOME:?HOME is required}/.local/share"}
case "$cache_root" in /*) ;; *) echo "cache root must be absolute" >&2; exit 1;; esac
cache_file="$cache_root/hash/plugin-data/io.runhash.autosuggestions/history.db"
test -f "$cache_file" && rm -- "$cache_file"
hash plugin enable io.runhash.autosuggestions
```

Do this only after Hash has exited. Live successful Hash history is queried on
every eligible edit and does not require a cache reset.

## Editor behavior and privacy

Suggestions are complete single-line strict extensions. Hash renders only the
dim suffix. Right fills the complete suggestion without running it; a later
Enter executes it. Tab continues to open interactive completion, Escape
dismisses the ghost, and Enter before acceptance executes only the visible
input. Long commands may wrap visually but remain one logical line.

Prompt-trigger predictions are always empty. Typed-prefix edit suggestions can
still appear after a failed command because they are successful-history
lookups, not next-command predictions. Unsafe, malformed, oversized, sensitive,
or non-UTF-8 entries are discarded. No telemetry or network access is used.

Autosuggestions can coexist with `io.runhash.adaptive-prediction`. Hash checks
providers in `[plugins].enabled` order; put the preferred provider first. An
empty or invalid result falls through to the next provider. Adaptive prediction
serves successful next-command sequences at prompt time, while autosuggestions
serves nonempty typed prefixes during edit time.
