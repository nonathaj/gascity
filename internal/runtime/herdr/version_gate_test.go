package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubHerdr writes a fake `herdr` binary that prints the given version string
// for `--version` (and exits 1 for anything else, which is fine for these
// tests — they only exercise the version gate).
func stubHerdr(t *testing.T, versionOut string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' \"" + versionOut + "\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// The version gate must fail fast (not a start→quarantine loop) when herdr is
// below the 0.7.5 interface this provider targets, and must pass on 0.7.5+.
func TestRequireSupportedVersion(t *testing.T) {
	cases := []struct {
		name       string
		versionOut string
		wantErr    bool
		wantSubstr string
	}{
		{"supported 0.7.5", "herdr 0.7.5", false, ""},
		{"supported newer", "herdr 0.8.1", false, ""},
		{"below min 0.7.4", "herdr 0.7.4", true, "below the supported minimum"},
		{"below min 0.7.1", "herdr 0.7.1", true, "below the supported minimum"},
		{"unparseable", "not a version", true, "cannot determine herdr version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := newClient("vtest", "")
			cl.bin = stubHerdr(t, c.versionOut)
			err := cl.requireSupportedVersion()
			if c.wantErr {
				if err == nil {
					t.Fatalf("requireSupportedVersion(%q) = nil, want error", c.versionOut)
				}
				if !strings.Contains(err.Error(), c.wantSubstr) {
					t.Fatalf("requireSupportedVersion(%q) error %q missing %q", c.versionOut, err, c.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("requireSupportedVersion(%q) = %v, want nil", c.versionOut, err)
			}
		})
	}
}

// startServer must surface the version gate error rather than launching a
// server it cannot drive (the quarantine-loop failure mode).
func TestStartServerFailsFastOnUnsupportedVersion(t *testing.T) {
	shortHome(t)
	cl := newClient("vtest-failfast", "")
	cl.bin = stubHerdr(t, "herdr 0.7.4")
	if err := cl.startServer(); err == nil {
		t.Fatal("startServer on herdr 0.7.4 = nil, want unsupported-version error")
	} else if !strings.Contains(err.Error(), "below the supported minimum") {
		t.Fatalf("startServer error %q missing version-gate message", err)
	}
}
