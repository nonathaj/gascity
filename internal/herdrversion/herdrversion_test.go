package herdrversion

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in              string
		maj, min, patch int
		raw             string
		pre             bool
		wantErr         bool
	}{
		{"herdr 0.7.5", 0, 7, 5, "0.7.5", false, false},
		{"herdr 0.7.4", 0, 7, 4, "0.7.4", false, false},
		{"0.7.5", 0, 7, 5, "0.7.5", false, false},
		{"v0.7.5", 0, 7, 5, "0.7.5", false, false},
		{"herdr 0.7.5\n", 0, 7, 5, "0.7.5", false, false},
		{"herdr 1.2.3", 1, 2, 3, "1.2.3", false, false},
		{"herdr 0.7.5-rc1", 0, 7, 5, "0.7.5-rc1", true, false},
		{"herdr 0.7.5+build.4", 0, 7, 5, "0.7.5", false, false},
		{"", 0, 0, 0, "", false, true},
		{"herdr", 0, 0, 0, "", false, true},
		{"herdr x.y.z", 0, 0, 0, "", false, true},
	}
	for _, c := range cases {
		info, err := Parse(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("Parse(%q): want error, got %+v", c.in, info)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): unexpected error %v", c.in, err)
			continue
		}
		if info.Major != c.maj || info.Minor != c.min || info.Patch != c.patch {
			t.Errorf("Parse(%q) = %d.%d.%d, want %d.%d.%d", c.in, info.Major, info.Minor, info.Patch, c.maj, c.min, c.patch)
		}
		if info.Raw != c.raw {
			t.Errorf("Parse(%q).Raw = %q, want %q", c.in, info.Raw, c.raw)
		}
		if info.PreRelease != c.pre {
			t.Errorf("Parse(%q).PreRelease = %v, want %v", c.in, info.PreRelease, c.pre)
		}
	}
}

func TestCompare(t *testing.T) {
	mk := func(ma, mi, pa int) Info { return Info{Major: ma, Minor: mi, Patch: pa} }
	cases := []struct {
		a, b Info
		want int
	}{
		{mk(0, 7, 4), mk(0, 7, 5), -1},
		{mk(0, 7, 5), mk(0, 7, 4), 1},
		{mk(0, 7, 5), mk(0, 7, 5), 0},
		{mk(1, 0, 0), mk(0, 7, 5), 1},
		{mk(0, 7, 4), mk(0, 8, 0), -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%v,%v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// The interface boundary: 0.7.4 and earlier use the legacy headless `agent
// start`; 0.7.5+ use the new --kind/--pane form.
func TestSupportsLegacyStart(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"herdr 0.7.1", true},
		{"herdr 0.7.4", true},
		{"herdr 0.7.5", false},
		{"herdr 0.8.0", false},
		{"herdr 1.0.0", false},
	}
	for _, c := range cases {
		info, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if got := info.SupportsLegacyStart(); got != c.want {
			t.Errorf("SupportsLegacyStart(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
