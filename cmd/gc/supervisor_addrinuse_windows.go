//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// platformAddrInUse reports the Windows spelling of "address already in use":
// winsock returns WSAEADDRINUSE (10048), which syscall.EADDRINUSE does not
// match on this platform.
func platformAddrInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}
