package beads

import "testing"

func TestIDHasPrefix(t *testing.T) {
	for _, tc := range []struct {
		id, prefix string
		want       bool
	}{
		{id: "gcty-iazh", prefix: "gcty", want: true},
		{id: "gcty-wisp-2ifhusy", prefix: "gcty", want: true},
		{id: "GCTY-IAZH", prefix: "gcty", want: true},
		{id: " gcty-iazh ", prefix: "gcty", want: true},
		{id: "gcty-iazh", prefix: "-GCTY- ", want: true},
		{id: "fe-926306", prefix: "gcty", want: false},
		{id: "gctyx-1", prefix: "gcty", want: false},
		{id: "gcty", prefix: "gcty", want: false},
		{id: "gcty-1", prefix: "", want: false},
		{id: "", prefix: "gcty", want: false},
	} {
		if got := IDHasPrefix(tc.id, tc.prefix); got != tc.want {
			t.Errorf("IDHasPrefix(%q, %q) = %t, want %t", tc.id, tc.prefix, got, tc.want)
		}
	}
}
