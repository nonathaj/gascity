package config

import (
	"os"
	slashpath "path"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/pathutil"
)

// ResolveSessionSetupScriptPath resolves a session_setup_script path for
// runtime and validation. Paths prefixed with "//" resolve against cityPath.
// Relative paths resolve against sourceDir when present, with legacy
// city-root-relative strings still supported during the transition.
//
// Pack config script paths are slash-form (portable), so resolution runs in
// slash-form (P4): a POSIX-absolute script is recognized as absolute on Windows
// too, and the result is slash-separated on every platform. Forward-slash paths
// still resolve for os.Stat/exec on Windows, so callers are unaffected.
func ResolveSessionSetupScriptPath(cityPath, sourceDir, script string) string {
	if script == "" {
		return ""
	}
	cityPath = filepath.ToSlash(cityPath)
	sourceDir = filepath.ToSlash(sourceDir)
	script = filepath.ToSlash(script)

	if strings.HasPrefix(script, "//") {
		return slashpath.Join(cityPath, strings.TrimPrefix(script, "//"))
	}
	if isSlashAbs(script) {
		return script
	}
	if sourceDir != "" {
		if relSource, err := filepath.Rel(cityPath, sourceDir); err == nil {
			relSource = filepath.ToSlash(filepath.Clean(relSource))
			cleanScript := slashpath.Clean(script)
			if relSource != "." && relSource != "" && !strings.HasPrefix(relSource, "..") &&
				(cleanScript == relSource || strings.HasPrefix(cleanScript, relSource+"/")) {
				return slashpath.Join(cityPath, cleanScript)
			}
		}

		sourceCandidate := slashpath.Join(sourceDir, script)
		cityCandidate := slashpath.Join(cityPath, slashpath.Clean(script))
		if sessionSetupScriptPathExists(cityCandidate) && !sessionSetupScriptPathExists(sourceCandidate) {
			return cityCandidate
		}
		return sourceCandidate
	}
	return slashpath.Join(cityPath, script)
}

// isSlashAbs reports whether a slash-form path is absolute (POSIX root or
// Windows drive-absolute). Delegates to the centralized pathutil.IsPortableAbs.
func isSlashAbs(p string) bool {
	return pathutil.IsPortableAbs(p)
}

func sessionSetupScriptPathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
