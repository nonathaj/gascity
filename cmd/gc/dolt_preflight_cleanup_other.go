//go:build !windows

package main

// platformFileOpenState is a no-op on non-Windows platforms: fileOpenedByAnyProcess
// determines the open state from the unix socket table, /proc, and lsof there.
func platformFileOpenState(string) (open bool, checked bool, err error) {
	return false, false, nil
}
