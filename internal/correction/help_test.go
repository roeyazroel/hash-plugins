package correction

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("HASH_TEST_HELP_MODE") {
	case "vocabulary":
		fmt.Print("Usage: generic-tool COMMAND\n\nAvailable Commands:\n  run    run it\n  ps     list it\n  pull   fetch it\n\nGlobal Options:\n  --help show help\n")
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDiscoverCommandVocabularyUsesOnlyPrescribedHelp(t *testing.T) {
	t.Setenv("HASH_TEST_HELP_MODE", "vocabulary")
	tool := os.Args[0]
	name := filepath.Base(tool)
	got, err := discoverCommandVocabulary(context.Background(), tool, "unknown command: "+name+" pe\nRun '"+name+" --help' for more information", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "ps", "pull"}
	if !slices.Equal(got, want) {
		t.Fatalf("vocabulary = %v, want %v", got, want)
	}
}

func TestDiscoverCommandVocabularyRejectsUnrelatedHelpHintWithoutExecution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	tool := writeHelpTool(t, "#!/bin/sh\ntouch \""+marker+"\"\n")
	got, err := DiscoverCommandVocabulary(context.Background(), tool, "Run 'other-tool --help' for more information")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("vocabulary = %v, want none", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unrelated help hint executed the tool: %v", err)
	}
}

func TestDiscoverCommandVocabularyHonorsContextDeadline(t *testing.T) {
	t.Setenv("HASH_TEST_HELP_MODE", "sleep")
	tool := os.Args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := DiscoverCommandVocabulary(ctx, tool, "Run '"+filepath.Base(tool)+" --help'"); err == nil {
		t.Fatal("timed-out help command returned no error")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("timed-out help command took %v", elapsed)
	}
}

func writeHelpTool(t *testing.T, body string) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "generic-tool")
	if err := os.WriteFile(tool, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return tool
}
