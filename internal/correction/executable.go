package correction

import (
	"os"
	"path/filepath"
	"sort"
)

const (
	maxPATHDirectories = 64
	maxPATHExecutables = 4096
)

// DiscoverExecutables returns a bounded, deterministic snapshot of executable
// names from absolute PATH directories. The snapshot is local-only and is used
// as candidate evidence when a command never started and emitted no diagnostic.
func DiscoverExecutables(pathValue string) []string {
	seenDirectories := make(map[string]bool)
	seenNames := make(map[string]bool)
	names := make([]string, 0, 256)

	for _, directoryPath := range filepath.SplitList(pathValue) {
		if len(seenDirectories) >= maxPATHDirectories || len(names) >= maxPATHExecutables {
			break
		}
		directoryPath = filepath.Clean(directoryPath)
		if !filepath.IsAbs(directoryPath) || seenDirectories[directoryPath] {
			continue
		}
		seenDirectories[directoryPath] = true

		directory, err := os.Open(directoryPath)
		if err != nil {
			continue
		}
		entries, _ := directory.ReadDir(maxPATHExecutables - len(names))
		_ = directory.Close()
		for _, entry := range entries {
			name := entry.Name()
			if seenNames[name] || !validDiagnosticCandidate(name) {
				continue
			}
			info, statErr := os.Stat(filepath.Join(directoryPath, name))
			if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				continue
			}
			seenNames[name] = true
			names = append(names, name)
			if len(names) >= maxPATHExecutables {
				break
			}
		}
	}

	sort.Strings(names)
	return names
}
