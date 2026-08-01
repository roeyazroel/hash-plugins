package correction

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDiscoverExecutablesIsBoundedAndFiltersEntries(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("git", 0o755)
	write("gpt", 0o755)
	write("not-executable", 0o644)
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := DiscoverExecutables(dir + string(os.PathListSeparator) + dir)
	if want := []string{"git", "gpt"}; !slices.Equal(got, want) {
		t.Fatalf("DiscoverExecutables() = %v, want %v", got, want)
	}
}
