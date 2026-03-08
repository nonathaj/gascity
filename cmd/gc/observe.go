package main

import (
	"os"
	"path/filepath"
	"strings"
)

// claudeProjectSlug converts an absolute path to the Claude project
// directory slug convention: all "/" and "." are replaced with "-".
func claudeProjectSlug(absPath string) string {
	s := strings.ReplaceAll(absPath, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// findJSONLSessionFile searches for the most recently modified JSONL
// session file matching workDir's slug in the given search paths.
// Returns "" if no matching file is found.
func findJSONLSessionFile(searchPaths []string, workDir string) string {
	slug := claudeProjectSlug(workDir)
	for _, base := range searchPaths {
		dir := filepath.Join(base, slug)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var bestPath string
		var bestTime int64
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			mt := info.ModTime().UnixNano()
			if mt > bestTime {
				bestTime = mt
				bestPath = filepath.Join(dir, e.Name())
			}
		}
		if bestPath != "" {
			return bestPath
		}
	}
	return ""
}

// defaultObservePaths returns the default search paths for Claude JSONL
// session files.
func defaultObservePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".claude", "projects")}
}

// observeSearchPaths merges default paths with user-configured extra
// paths, expanding ~ and deduplicating.
func observeSearchPaths(extraPaths []string) []string {
	seen := make(map[string]bool)
	var result []string
	add := func(p string) {
		// Expand leading ~.
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	for _, p := range defaultObservePaths() {
		add(p)
	}
	for _, p := range extraPaths {
		add(p)
	}
	return result
}
