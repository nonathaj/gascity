package execshim

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveExecutable pins the path-qualified resolution rule: an
// existing file resolves to itself even without a Windows-executable
// extension (Command runs shebang scripts through sh), a directory or
// missing path errors, and bare names go through LookPath.
func TestResolveExecutable(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "provider")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	if got, err := ResolveExecutable(script); err != nil || got != script {
		t.Fatalf("ResolveExecutable(existing extensionless) = %q, %v; want identity, nil", got, err)
	}
	if _, err := ResolveExecutable(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("ResolveExecutable(missing) = nil error, want error")
	}
	if _, err := ResolveExecutable(dir); err == nil {
		t.Fatal("ResolveExecutable(directory) = nil error, want error")
	}
	if got, err := ResolveExecutable("true"); err != nil || got == "" {
		t.Fatalf("ResolveExecutable(bare coreutil) = %q, %v; want LookPath resolution", got, err)
	}
}

// TestIsGoTestExecutable pins the anti-re-exec guard across platform
// binary spellings. On Windows test binaries end in ".test.exe", and a
// guard that missed that spelling let the submit poller spawn the test
// binary itself, which re-ran the whole suite per spawn — a fork bomb
// (incident gw-8g5: 4,500 processes, ~246 GB commit in ~10 minutes).
func TestIsGoTestExecutable(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/tmp/go-build123/b001/session.test", true},
		{`C:\Users\u\AppData\Local\Temp\go-build123\b001\session.test.exe`, true},
		{`C:\t\SESSION.TEST.EXE`, true}, // Windows filesystems are case-insensitive
		{"session.test", true},
		{"session.test.exe", true},
		{"/usr/local/bin/gc", false},
		{`C:\Program Files\gc\gc.exe`, false},
		{"gc", false},
		{"gc.exe", false},
		{"contest", false}, // ".test" must be a suffix segment, not a substring
		{"contest.exe", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsGoTestExecutable(tc.path); got != tc.want {
			t.Errorf("IsGoTestExecutable(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
