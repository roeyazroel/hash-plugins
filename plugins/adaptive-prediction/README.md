# Hash Adaptive Prediction

This plugin learns exact successful command transitions locally. It only
suggests `A -> B` after Hash reports that `A` exited zero; failures, signals,
interruptions, and cancellations clear the context and are handled by the
autocorrection plugin instead. The first acceptance action only fills the
editor.

Build and link it during development:

```sh
go build -trimpath -o /tmp/hash-adaptive-prediction ./plugins/adaptive-prediction
mkdir -p /tmp/hash-adaptive-prediction-bundle
cp /tmp/hash-adaptive-prediction plugins/adaptive-prediction/hash-plugin.toml /tmp/hash-adaptive-prediction-bundle/
hash plugin link /tmp/hash-adaptive-prediction-bundle
hash plugin enable io.runhash.adaptive-prediction
hash plugin doctor io.runhash.adaptive-prediction
```

Configure it in `~/.config/hash/config.toml`:

```toml
[prediction]
enabled = false

[plugins]
enabled = ["io.runhash.adaptive-prediction"]

[plugins.settings."io.runhash.adaptive-prediction"]
confidence_threshold = 0.6
learn_from_other_shells = false
shells = ["zsh", "bash", "fish"]
```

Run two successful commands repeatedly, for example `git status` followed by
`git pull`. On the next prompt, Right fills the predicted line and does not
execute it; a separate Enter submits. After a failed command no prediction is
shown. To roll back, run:

```sh
hash plugin disable io.runhash.adaptive-prediction
hash plugin uninstall io.runhash.adaptive-prediction   # managed bundles only
rm -rf "$XDG_DATA_HOME/hash/plugin-data/io.runhash.adaptive-prediction" # explicit data reset
```

Uninstall preserves the database. Delete it only after exiting Hash when a
fresh one-time cross-shell import is desired. No network access or telemetry
is used.
