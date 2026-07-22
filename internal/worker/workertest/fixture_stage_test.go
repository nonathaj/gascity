package workertest

import (
	"crypto/md5" //nolint:gosec // mirrors Kimi's workdir storage key (md5), not security.
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/overlay"
)

// stageWorkdirHashedFixture makes a workdir-hash-keyed transcript fixture
// discoverable on the current OS. Kimi keys its session store by
// md5(filepath.Clean(workDir)) — see internal/sessionlog.kimiWorkDirHash — and
// filepath.Clean is OS-native: a fixture recorded on Linux is committed under
// the forward-slash-cleaned hash, which no longer matches on Windows where
// Clean yields backslashes and thus a different hash. When the platform hash
// differs from the committed sessions/<hash> directory, the fixture is copied
// into a temp root under the platform hash so discovery — using the same hash
// function — finds it. This exercises the discovery/storage-key contract itself
// on every OS without hardcoding a per-OS hash. Fixtures without a sessions/
// layout (every provider except Kimi) are returned unchanged.
func stageWorkdirHashedFixture(t *testing.T, profile Profile, fixtureRoot string) string {
	t.Helper()
	sessionsDir := filepath.Join(fixtureRoot, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return fixtureRoot // not a workdir-hashed fixture layout
	}
	want := fixtureWorkDirHash(profile.WorkDir)
	committed := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.EqualFold(e.Name(), want) {
			return fixtureRoot // already discoverable on this platform
		}
		committed = append(committed, e)
	}
	if len(committed) != 1 || want == "" {
		return fixtureRoot // ambiguous/empty layout: let discovery report it
	}

	staged := t.TempDir()
	src := filepath.Join(sessionsDir, committed[0].Name())
	dst := filepath.Join(staged, "sessions", want)
	if err := os.MkdirAll(dst, 0o750); err != nil {
		t.Fatalf("stage workdir-hashed fixture for %s: %v", profile.ID, err)
	}
	if err := overlay.CopyDir(src, dst, io.Discard); err != nil {
		t.Fatalf("stage workdir-hashed fixture for %s: %v", profile.ID, err)
	}
	return staged
}

// fixtureWorkDirHash mirrors internal/sessionlog.kimiWorkDirHash: the md5 of the
// OS-native cleaned workDir. If the two ever diverge the staged fixture becomes
// unreachable and the conformance run fails, which is the intended tripwire.
func fixtureWorkDirHash(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	sum := md5.Sum([]byte(filepath.Clean(workDir))) //nolint:gosec // storage key, not security.
	return hex.EncodeToString(sum[:])
}
