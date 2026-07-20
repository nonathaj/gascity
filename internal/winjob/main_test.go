package winjob

import (
	"testing"

	"github.com/gastownhall/gascity/internal/testutil"
)

// TestMain enforces a hard lifetime bound on this test binary so a
// killed `go test` run cannot orphan it on Windows (incident gw-qhs).
func TestMain(m *testing.M) {
	testutil.RunWithWatchdog(m)
}
