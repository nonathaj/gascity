package execshim

import "testing"

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
