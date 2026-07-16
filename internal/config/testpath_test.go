package config

import "runtime"

// absFixturePath converts a unix-style absolute fixture path (e.g. "/city")
// into one that filepath.IsAbs accepts on the current platform. On Windows a
// rooted path without a volume is NOT absolute, so the fixture gains a "C:"
// volume while keeping forward slashes (which every filepath function
// accepts); elsewhere the path is returned unchanged. Tests that assert on
// derived values compare after filepath.ToSlash so the same unix-style
// expectations hold on both platforms.
func absFixturePath(p string) string {
	if runtime.GOOS == "windows" {
		return "C:" + p
	}
	return p
}
