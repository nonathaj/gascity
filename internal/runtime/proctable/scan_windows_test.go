//go:build windows

package proctable

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
	"unicode/utf16"
)

func encodeEnvBlock(entries ...string) []byte {
	var u16 []uint16
	for _, e := range entries {
		u16 = append(u16, utf16.Encode([]rune(e))...)
		u16 = append(u16, 0)
	}
	u16 = append(u16, 0) // block terminator
	out := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(out[2*i:], v)
	}
	return out
}

func TestParseUTF16EnvBlock(t *testing.T) {
	block := encodeEnvBlock(
		"GC_SESSION_ID=gw-abc",
		"=C:=C:\\Users\\dev", // drive bookkeeping entry: skipped (empty key)
		"PATH=C:\\Windows",
		"EMPTY=",
	)
	env := parseUTF16EnvBlock(block)
	if env["GC_SESSION_ID"] != "gw-abc" {
		t.Errorf("GC_SESSION_ID = %q", env["GC_SESSION_ID"])
	}
	if env["PATH"] != `C:\Windows` {
		t.Errorf("PATH = %q", env["PATH"])
	}
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("EMPTY = %q ok=%v, want present and empty", v, ok)
	}
	if len(env) != 3 {
		t.Errorf("len(env) = %d, want 3 (drive entry skipped): %v", len(env), env)
	}
}

func TestReadProcessEnvSelf(t *testing.T) {
	marker := fmt.Sprintf("gc-proctable-selftest-%d", time.Now().UnixNano())
	t.Setenv("GC_PROCTABLE_SELFTEST", marker)
	// t.Setenv mutates this process's env; a fresh PEB read must NOT see it
	// (the PEB block is fixed at process start), so assert on a variable
	// that existed at startup instead.
	env, err := readProcessEnv(os.Getpid())
	if err != nil {
		t.Fatalf("readProcessEnv(self): %v", err)
	}
	if len(env) == 0 {
		t.Fatal("self environment read returned no variables")
	}
	// PATH (or Path) is always present in a real process environment.
	if _, ok := env["PATH"]; !ok {
		if _, ok := env["Path"]; !ok {
			t.Fatalf("self environment missing PATH; got %d vars", len(env))
		}
	}
}

func TestWinRecordIsSessionRoot(t *testing.T) {
	records := map[int]winRecord{
		100: {pid: 100, ppid: 4, command: "gc.exe", env: map[string]string{}},
		200: {pid: 200, ppid: 100, command: "sh.exe", env: map[string]string{"GC_SESSION_ID": "gw-a"}},
		201: {pid: 201, ppid: 200, command: "claude.exe", env: map[string]string{"GC_SESSION_ID": "gw-a"}},
		300: {pid: 300, ppid: 100, command: "psmux.exe", env: map[string]string{"GC_SESSION_ID": "gw-b"}},
		301: {pid: 301, ppid: 300, command: "node.exe", env: map[string]string{"GC_SESSION_ID": "gw-b"}},
		400: {pid: 400, ppid: 999, command: "codex.exe", env: map[string]string{"GC_SESSION_ID": "gw-c"}},
	}
	tests := []struct {
		name string
		pid  int
		id   string
		want bool
	}{
		{"root of its session", 200, "", true},
		{"child inside session envelope", 201, "", false},
		{"child of tmux infrastructure is a root", 301, "", true},
		{"orphan with dead parent is a root", 400, "", true},
		{"id filter matches", 200, "gw-a", true},
		{"id filter rejects other sessions", 200, "gw-b", false},
		{"no session id is never a root", 100, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := winRecordIsSessionRoot(records, records[tt.pid], tt.id); got != tt.want {
				t.Errorf("winRecordIsSessionRoot(pid %d, id %q) = %v, want %v", tt.pid, tt.id, got, tt.want)
			}
		})
	}
}

func TestKillByPIDWithConfirmsDeath(t *testing.T) {
	if err := killByPIDWith(1, nil, nil, time.Millisecond, time.Millisecond); err == nil {
		t.Fatal("PID 1 must be refused")
	}

	// Already gone: success without any kill call.
	killed := false
	err := killByPIDWith(50, func(int, bool) error { killed = true; return nil },
		func(int) bool { return false }, time.Millisecond, time.Millisecond)
	if err != nil || killed {
		t.Fatalf("already-dead pid: err=%v killed=%v", err, killed)
	}

	// Dies during the graceful grace window.
	var forced []bool
	aliveUntilGraceful := true
	err = killByPIDWith(51, func(_ int, force bool) error {
		forced = append(forced, force)
		aliveUntilGraceful = false
		return nil
	}, func(int) bool { return aliveUntilGraceful }, 50*time.Millisecond, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("graceful death: %v", err)
	}
	if len(forced) != 1 || forced[0] {
		t.Fatalf("kill calls = %v, want one graceful", forced)
	}

	// Survives everything: confirmed-dead contract fails loudly.
	err = killByPIDWith(52, func(int, bool) error { return nil },
		func(int) bool { return true }, time.Millisecond, time.Millisecond)
	if err == nil || !errors.Is(err, err) {
		t.Fatal("immortal pid must return an error")
	}
}
