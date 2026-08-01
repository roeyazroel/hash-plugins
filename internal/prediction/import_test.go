package prediction

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportParsesBashZshAndFishHistory(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{
		"bash": filepath.Join(root, "bash_history"),
		"zsh":  filepath.Join(root, "zsh_history"),
		"fish": filepath.Join(root, "fish_history"),
	}
	if err := os.WriteFile(paths["bash"], []byte("git status\ngit pull\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths["zsh"], []byte(": 1700000000:0;make test\n: 1700000001:0;make build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths["fish"], []byte("- cmd: git status\n  when: 1700000000\n- cmd: git pull\n  when: 1700000001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sequences, err := importHistories(paths, []string{"bash", "zsh", "fish"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sequences) != 3 {
		t.Fatalf("imported sequences = %d, want 3", len(sequences))
	}
}

func TestImportSkipsSensitiveAndInvalidEntriesWithoutBridging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bash_history")
	if err := os.WriteFile(path, []byte("git status\nexport API_TOKEN=secret\ngit pull\necho 'unterminated\nmake test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sequences, err := importHistories(map[string]string{"bash": path}, []string{"bash"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sequences) != 0 {
		t.Fatalf("sensitive/invalid entries bridged into %d sequences", len(sequences))
	}
}

func writeTestHistory(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}
