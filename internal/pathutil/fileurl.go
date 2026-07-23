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

// IsPortableAbs reports whether a config-authored path is absolute in either
// spelling: native (filepath.IsAbs) or POSIX/slash-form ("/home/x", "C:/x").
// Config-authored paths are slash-form on every platform (doctrine P4), and on
// Windows filepath.IsAbs alone is false for "/home/x" — code that joins
// "relative" paths under a root would silently corrupt a POSIX-absolute config
// value there. Centralized after the same bug appeared in three resolvers
// (session setup scripts, rig paths in cmd/gc and importsvc).
func IsPortableAbs(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	p := filepath.ToSlash(path)
	if strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && p[2] == '/'
}
