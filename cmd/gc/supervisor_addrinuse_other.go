//go:build !windows

package main

// platformAddrInUse is a no-op off Windows: syscall.EADDRINUSE matches there.
func platformAddrInUse(error) bool { return false }
