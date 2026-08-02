package conformance

import (
	"encoding/json"
	"os"
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
		`version = "0.2.4"`,
		`entrypoint = "hash-autosuggestions"`,
		`host_services = ["history.query"]`,
	)
	assertFileContains(t, filepath.Join(root, ".goreleaser.yaml"),
		`id: hash-autosuggestions`,
		`main: ./plugins/autosuggestions`,
		`name_template: "hash-autosuggestions_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`,
		`src: plugins/autosuggestions/hash-plugin.toml`,
	)
	assertFileContains(t, filepath.Join(root, "HASH_PLUGINS.json"),
		`"io.runhash.autosuggestions"`,
		`hash-autosuggestions_{{version}}_darwin_amd64.tar.gz`,
		`hash-autosuggestions_{{version}}_darwin_arm64.tar.gz`,
		`hash-autosuggestions_{{version}}_linux_amd64.tar.gz`,
		`hash-autosuggestions_{{version}}_linux_arm64.tar.gz`,
	)
	assertFileContains(t, filepath.Join(root, "scripts", "verify.sh"),
		`test -x "$build_dir/hash-autosuggestions"`,
		`test -f plugins/autosuggestions/hash-plugin.toml`,
		`for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64`,
		`tar -tzf "$autosuggestions_archive" | grep -qx 'hash-autosuggestions'`,
		`tar -tzf "$autosuggestions_archive" | grep -qx 'hash-plugin.toml'`,
		`json.load`,
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
	assertAutosuggestionsIndex(t, filepath.Join(root, "HASH_PLUGINS.json"))
}

func TestShippedPluginReleaseVersionsMatch(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range []string{
		"autocorrection",
		"adaptive-prediction",
		"autosuggestions",
	} {
		assertFileContains(t, filepath.Join(root, "plugins", plugin, "hash-plugin.toml"),
			`version = "0.2.4"`,
		)
	}
}

func assertAutosuggestionsIndex(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Plugins map[string]struct {
			Artifacts map[string]struct {
				Name string `json:"name"`
			} `json:"artifacts"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	artifacts := index.Plugins["io.runhash.autosuggestions"].Artifacts
	expected := map[string]string{
		"darwin/amd64": "hash-autosuggestions_{{version}}_darwin_amd64.tar.gz",
		"darwin/arm64": "hash-autosuggestions_{{version}}_darwin_arm64.tar.gz",
		"linux/amd64":  "hash-autosuggestions_{{version}}_linux_amd64.tar.gz",
		"linux/arm64":  "hash-autosuggestions_{{version}}_linux_arm64.tar.gz",
	}
	if len(artifacts) != len(expected) {
		t.Fatalf("autosuggestions artifacts = %v", artifacts)
	}
	for platform, name := range expected {
		if artifacts[platform].Name != name {
			t.Errorf("artifact %s = %q, want %q", platform, artifacts[platform].Name, name)
		}
	}
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
