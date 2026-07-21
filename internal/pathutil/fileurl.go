package pathutil

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// LocalPathFromFileURL converts a parsed file:// URL to a native local
// path. file:///C:/x parses to a Path of "/C:/x"; the leading slash of
// a drive-lettered path is stripped so the result is a real Windows
// path — the naive filepath.FromSlash(u.Path) produced "\C:\x", a bug
// found independently in three call sites before being centralized
// here (doctrine class P3). URLs with a host other than empty or
// "localhost" are not local paths and are rejected; callers with
// stricter policies layer them on top.
func LocalPathFromFileURL(u *url.URL) (string, error) {
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("file URL host %q is not a local path", u.Host)
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), nil
}

// FileURLForLocalPath renders a local path in the sanctioned file URL
// spelling: three slashes and a POSIX-form path ("file:///C:/x" on
// Windows — "file://C:/x" would parse "C:" as a host).
func FileURLForLocalPath(path string) string {
	return "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
}
