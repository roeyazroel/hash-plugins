package conformance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutosuggestionsShippingMetadata(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "plugins", "autosuggestions", "hash-plugin.toml"),
		`id = "io.runhash.autosuggestions"`,
		`version = "1.0.0"`,
		`entrypoint = "hash-autosuggestions"`,
		`host_services = ["history.query"]`,
	)
	assertFileContains(t, filepath.Join(root, "HASH_PLUGINS.json"),
		`"schema_version": 2`,
		`"release_tag": "autosuggestions-v1.0.0"`,
		`hash-autosuggestions_1.0.0_darwin_amd64.tar.gz`,
		`hash-autosuggestions_1.0.0_darwin_arm64.tar.gz`,
		`hash-autosuggestions_1.0.0_linux_amd64.tar.gz`,
		`hash-autosuggestions_1.0.0_linux_arm64.tar.gz`,
	)
	assertFileContains(t, filepath.Join(root, "scripts", "verify.sh"),
		`test -x "$build_dir/hash-autosuggestions"`,
		`test -f plugins/autosuggestions/hash-plugin.toml`,
		`package-plugin.sh`,
		`shasum -a 256 -c SHA256SUMS`,
	)
	assertFileContains(t, filepath.Join(root, "README.md"),
		`io.runhash.autosuggestions`,
		`plugins/autosuggestions/README.md`,
	)
	assertFileContains(t, filepath.Join(root, "plugins", "autosuggestions", "README.md"),
		`learn_from_other_shells = false`,
		`learn_from_other_shells = true`,
		`shells = ["zsh", "bash", "fish"]`,
		`history_limit = 100`,
		`history.db`,
		`${XDG_DATA_HOME:-"${HOME:?HOME is required}/.local/share"}`,
		`Right`,
		`Tab`,
		`io.runhash.adaptive-prediction`,
	)
	assertReleaseIndexV2(t, filepath.Join(root, "HASH_PLUGINS.json"))
}

func TestShippedPluginReleaseVersionsAreIndependent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, filepath.Join(root, "plugins", "autocorrection", "hash-plugin.toml"), `version = "1.0.0"`)
	assertFileContains(t, filepath.Join(root, "plugins", "adaptive-prediction", "hash-plugin.toml"), `version = "1.0.0"`)
	assertFileContains(t, filepath.Join(root, "plugins", "autosuggestions", "hash-plugin.toml"), `version = "1.0.0"`)
	assertFileContains(t, filepath.Join(root, ".github", "workflows", "release-plugin.yml"),
		`autocorrection-v*`,
		`adaptive-prediction-v*`,
		`autosuggestions-v*`,
		`./scripts/release-plugin.sh`,
		`softprops/action-gh-release`,
		`make_latest: false`,
	)
	assertFileContains(t, filepath.Join(root, ".github", "workflows", "release-catalog.yml"),
		`catalog-v*`,
		`./scripts/release-catalog.sh`,
		`make_latest: true`,
	)
	assertFileContains(t, filepath.Join(root, "scripts", "package-plugin.sh"),
		`GOOS="$os" GOARCH="$arch"`,
		`HASH_PLUGINS.json`,
	)
	assertFileContains(t, filepath.Join(root, "scripts", "release-plugin.sh"),
		`autocorrection-v`,
		`adaptive-prediction-v`,
		`autosuggestions-v`,
		`SHA256SUMS`,
	)
	assertFileContains(t, filepath.Join(root, "scripts", "release-catalog.sh"),
		`catalog-v`,
		`HASH_PLUGINS.json`,
		`SHA256SUMS`,
	)
	if _, err := os.Stat(filepath.Join(root, ".goreleaser.yaml")); !os.IsNotExist(err) {
		t.Fatalf("obsolete shared-release GoReleaser config is still present: %v", err)
	}
	assertFileContains(t, filepath.Join(root, "README.md"),
		`hash plugin install github:roeyazroel/hash-plugins --all`,
		`hash plugin upgrade --all`,
		"`--id`, or install every catalog entry with `--all`",
		`catalog-v1.0.0`,
	)
	assertFileContains(t, filepath.Join(root, "plugins", "autocorrection", "README.md"),
		`--id io.runhash.autocorrection`,
	)
	assertFileContains(t, filepath.Join(root, "plugins", "adaptive-prediction", "README.md"),
		`--id io.runhash.adaptive-prediction`,
	)
	for _, plugin := range []string{"autocorrection", "adaptive-prediction", "autosuggestions"} {
		if strings.Contains(string(mustRead(t, filepath.Join(root, "plugins", plugin, "hash-plugin.toml"))), `version = "0.2.4"`) {
			t.Errorf("%s retains the shared release version", plugin)
		}
	}
}

func TestCatalogRejectsDuplicateReleaseTags(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	fixture := t.TempDir()
	for _, plugin := range []string{"autocorrection", "adaptive-prediction", "autosuggestions"} {
		manifestPath := filepath.Join(root, "plugins", plugin, "hash-plugin.toml")
		manifest, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		pluginDir := filepath.Join(fixture, "plugins", plugin)
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pluginDir, "hash-plugin.toml"), manifest, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var catalog map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(root, "HASH_PLUGINS.json")), &catalog); err != nil {
		t.Fatal(err)
	}
	plugins, ok := catalog["plugins"].(map[string]any)
	if !ok {
		t.Fatal("catalog plugins is not an object")
	}
	plugins["io.runhash.adaptive-prediction"].(map[string]any)["release_tag"] =
		plugins["io.runhash.autocorrection"].(map[string]any)["release_tag"]
	catalogRaw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "HASH_PLUGINS.json"), catalogRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("python3", filepath.Join(root, "scripts", "validate-catalog.py"), fixture)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("validator accepted duplicate release tags; output: %s", output)
	}
	if !strings.Contains(string(output), "duplicate release_tag") {
		t.Fatalf("validator error = %q, want duplicate release_tag", output)
	}
}

func assertReleaseIndexV2(t *testing.T, path string) {
	t.Helper()
	raw := mustRead(t, path)
	var index struct {
		SchemaVersion int `json:"schema_version"`
		Plugins       map[string]struct {
			Version    string `json:"version"`
			ReleaseTag string `json:"release_tag"`
			Artifacts  map[string]struct {
				Name string `json:"name"`
			} `json:"artifacts"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	if index.SchemaVersion != 2 {
		t.Fatalf("schema_version = %d, want 2", index.SchemaVersion)
	}
	expected := map[string]struct {
		version, releaseTag, archivePrefix string
	}{
		"io.runhash.autocorrection":      {"1.0.0", "autocorrection-v1.0.0", "hash-autocorrection_1.0.0"},
		"io.runhash.adaptive-prediction": {"1.0.0", "adaptive-prediction-v1.0.0", "hash-adaptive-prediction_1.0.0"},
		"io.runhash.autosuggestions":     {"1.0.0", "autosuggestions-v1.0.0", "hash-autosuggestions_1.0.0"},
	}
	if len(index.Plugins) != len(expected) {
		t.Fatalf("plugins = %v", index.Plugins)
	}
	for id, want := range expected {
		entry, ok := index.Plugins[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		if entry.Version != want.version || entry.ReleaseTag != want.releaseTag {
			t.Errorf("%s = version %q release_tag %q", id, entry.Version, entry.ReleaseTag)
		}
		for platform, suffix := range map[string]string{"darwin/amd64": "darwin_amd64", "darwin/arm64": "darwin_arm64", "linux/amd64": "linux_amd64", "linux/arm64": "linux_arm64"} {
			if got := entry.Artifacts[platform].Name; got != want.archivePrefix+"_"+suffix+".tar.gz" {
				t.Errorf("%s artifact %s = %q", id, platform, got)
			}
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertFileContains(t *testing.T, path string, values ...string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !strings.Contains(string(raw), value) {
			t.Errorf("%s does not contain %q", filepath.Base(path), value)
		}
	}
}
