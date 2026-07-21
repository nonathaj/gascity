package pathutil

import (
	"net/url"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLocalPathFromFileURL pins doctrine class P3: file:///C:/x parses
// to a url Path of "/C:/x", and the naive FromSlash produced "\C:\x" —
// a bug found independently in doctor backup state, packregistry, and
// the dolt registration reader before being centralized here.
func TestLocalPathFromFileURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string // slash-form; converted per-OS in the assertion
	}{
		{"file:///tmp/registry", "/tmp/registry"},
		{"file:///C:/Users/jane/cache", "C:/Users/jane/cache"},
		{"file:///D:/x", "D:/x"},
		{"file://localhost/tmp/x", "/tmp/x"},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.raw, err)
		}
		got, err := LocalPathFromFileURL(u)
		if err != nil {
			t.Fatalf("LocalPathFromFileURL(%q): %v", tc.raw, err)
		}
		if want := filepath.FromSlash(tc.want); got != want {
			t.Errorf("LocalPathFromFileURL(%q) = %q, want %q", tc.raw, got, want)
		}
	}
}

func TestLocalPathFromFileURLRejectsForeignHost(t *testing.T) {
	u, _ := url.Parse("file://fileserver/share/x")
	if _, err := LocalPathFromFileURL(u); err == nil {
		t.Fatal("foreign host accepted; file URLs with hosts are not local paths")
	}
}

// TestFileURLForLocalPath pins the sanctioned three-slash spelling:
// "file://C:/x" would parse "C:" as a host.
func TestFileURLForLocalPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/registry", "file:///tmp/registry"},
		{`C:\Users\jane\cache`, "file:///C:/Users/jane/cache"},
	}
	for _, tc := range cases {
		if runtime.GOOS != "windows" && tc.in[0] != '/' {
			continue // backslash spelling is a Windows-only input
		}
		if got := FileURLForLocalPath(tc.in); got != tc.want {
			t.Errorf("FileURLForLocalPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFileURLRoundTrip: a local path survives ToURL -> Parse -> ToPath.
func TestFileURLRoundTrip(t *testing.T) {
	p := t.TempDir()
	u, err := url.Parse(FileURLForLocalPath(p))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := LocalPathFromFileURL(u)
	if err != nil {
		t.Fatalf("LocalPathFromFileURL: %v", err)
	}
	if got != p {
		t.Fatalf("round trip = %q, want %q", got, p)
	}
}
