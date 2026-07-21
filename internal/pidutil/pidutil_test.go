package pidutil

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestAliveWithCmdlineVerifiesOwnProcess pins real command-line
// verification on the platforms that support it (linux via /proc,
// windows via the PEB): a matcher that inspects this test binary's own
// argv must be consulted — both positively and, critically, negatively.
// The old Windows fallback ignored the matcher entirely and reported
// any live PID as a match (gw-opy: poller ownership checks degraded to
// bare liveness, blind to PID reuse).
func TestAliveWithCmdlineVerifiesOwnProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("cmdline inspection unsupported on this platform (falls back to Alive)")
	}
	pid := os.Getpid()
	matched := AliveWithCmdline(pid, func(argv []string) bool {
		return len(argv) > 0 && strings.Contains(strings.ToLower(argv[0]), "test")
	})
	if !matched {
		t.Fatalf("AliveWithCmdline(self, arg0-contains-test) = false; argv[0] should name the test binary")
	}
	mismatched := AliveWithCmdline(pid, func(argv []string) bool {
		return ArgvContainsSequence(argv, "definitely-not-a-real-token-9a8b7c")
	})
	if mismatched {
		t.Fatal("AliveWithCmdline(self, impossible-token) = true; matcher was not consulted")
	}
}

// TestCmdlineOwnProcess pins the raw Cmdline read on supported
// platforms: the argv must be non-empty and normalized.
func TestCmdlineOwnProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("cmdline inspection unsupported on this platform")
	}
	argv, err := Cmdline(os.Getpid())
	if err != nil {
		t.Fatalf("Cmdline(self): %v", err)
	}
	if len(argv) == 0 {
		t.Fatal("Cmdline(self) = empty argv")
	}
	for _, arg := range argv {
		if strings.TrimSpace(arg) == "" {
			t.Fatalf("Cmdline(self) contains unnormalized empty arg: %q", argv)
		}
	}
}

func TestStartTimeRejectsInvalidPID(t *testing.T) {
	if _, err := StartTime(0); err == nil {
		t.Fatal("StartTime(0) = nil error, want error")
	}
}

func TestNormalizeArgv(t *testing.T) {
	got := NormalizeArgv([]string{"cut", "", "-d", " ", "\t ", "-f", "1"})
	want := []string{"cut", "-d", "-f", "1"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeArgv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeArgv = %q, want %q", got, want)
		}
	}
	if out := NormalizeArgv(nil); len(out) != 0 {
		t.Fatalf("NormalizeArgv(nil) = %q, want empty", out)
	}
}

func TestArgvContainsSequence(t *testing.T) {
	argv := []string{"gc", "nudge", "poll", "--city", "/tmp/city"}
	cases := []struct {
		name string
		seq  []string
		want bool
	}{
		{name: "empty sequence", seq: nil, want: true},
		{name: "contiguous sequence", seq: []string{"nudge", "poll"}, want: true},
		{name: "non-contiguous sequence", seq: []string{"gc", "poll"}, want: false},
		{name: "argv shorter than sequence", seq: []string{"gc", "nudge", "poll", "--city", "/tmp/city", "extra"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ArgvContainsSequence(argv, tc.seq...); got != tc.want {
				t.Fatalf("ArgvContainsSequence(%v, %v) = %v, want %v", argv, tc.seq, got, tc.want)
			}
		})
	}
}

func TestArgvHasFlagValue(t *testing.T) {
	argv := []string{"gc", "nudge", "poll", "--city", "/tmp/city-a", "--session=s-worker"}
	cases := []struct {
		name  string
		flag  string
		value string
		want  bool
	}{
		{name: "space form", flag: "--city", value: "/tmp/city-a", want: true},
		{name: "equals form", flag: "--session", value: "s-worker", want: true},
		{name: "wrong value", flag: "--city", value: "/tmp/city-b", want: false},
		{name: "empty flag", flag: "", value: "/tmp/city-a", want: false},
		{name: "empty value", flag: "--city", value: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ArgvHasFlagValue(argv, tc.flag, tc.value); got != tc.want {
				t.Fatalf("ArgvHasFlagValue(%v, %q, %q) = %v, want %v", argv, tc.flag, tc.value, got, tc.want)
			}
		})
	}
}
