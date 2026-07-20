//go:build !windows

package testenv

// containTestProcessTree is a no-op off Windows: process-tree teardown
// works there, and resource containment is owned by the systemd slice
// enrollment in scripts/lib/test-slice.sh.
func containTestProcessTree() {}
