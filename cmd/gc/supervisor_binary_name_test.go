package main

import (
	goruntime "runtime"
	"strings"
	"testing"
)

func TestSupervisorBinaryFileName(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{"linux", "gc"},
		{"darwin", "gc"},
		{"windows", "gc.exe"},
	}
	for _, tt := range tests {
		if got := supervisorBinaryFileName(tt.goos); got != tt.want {
			t.Errorf("supervisorBinaryFileName(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}

func TestStableSupervisorBinaryCandidatesUsePlatformFileName(t *testing.T) {
	want := supervisorBinaryFileName(goruntime.GOOS)
	for _, candidate := range stableSupervisorBinaryCandidates(t.TempDir(), t.TempDir()) {
		if !strings.HasSuffix(candidate, want) {
			t.Errorf("candidate %q should end with %q", candidate, want)
		}
	}
}
