package fsys

import (
	"io/fs"
	"runtime"
	"testing"
)

func TestIsExecutableModeCore(t *testing.T) {
	tests := []struct {
		name string
		goos string
		mode fs.FileMode
		want bool
	}{
		{"unix regular executable", "linux", 0o755, true},
		{"unix owner-only execute bit", "linux", 0o700, true},
		{"unix regular non-executable", "linux", 0o644, false},
		{"unix zero perms", "linux", 0, false},
		{"darwin follows unix rules", "darwin", 0o644, false},
		{"unix directory with exec bits", "linux", fs.ModeDir | 0o755, false},
		{"windows regular file qualifies", "windows", 0o666, true},
		{"windows regular file without perm bits", "windows", 0, true},
		{"windows directory does not qualify", "windows", fs.ModeDir | 0o777, false},
		{"windows device does not qualify", "windows", fs.ModeDevice | 0o666, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExecutableMode(tt.goos, tt.mode); got != tt.want {
				t.Errorf("isExecutableMode(%q, %v) = %v, want %v", tt.goos, tt.mode, got, tt.want)
			}
		})
	}
}

func TestIsExecutableModeMatchesPlatform(t *testing.T) {
	mode := fs.FileMode(0o644)
	want := runtime.GOOS == "windows"
	if got := IsExecutableMode(mode); got != want {
		t.Errorf("IsExecutableMode(0o644) = %v on %s, want %v", got, runtime.GOOS, want)
	}
}
