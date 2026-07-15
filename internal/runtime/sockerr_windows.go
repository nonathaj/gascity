//go:build windows

package runtime

import (
	"errors"
	"syscall"
)

// Winsock error codes, spelled as raw errnos because this package must stay
// stdlib-only (TestRuntimeContractPackageStaysStdlibOnly) and the syscall
// package's POSIX constants on Windows are legacy placeholder values that
// never match what net actually returns.
const (
	wsaeconnrefused = syscall.Errno(10061) // WSAECONNREFUSED
	wsaenetdown     = syscall.Errno(10050) // WSAENETDOWN
)

// platformUnavailableSocketError reports platform-specific "session is dead"
// dial errors. Winsock refuses AF_UNIX connections to a dead socket with
// WSAECONNREFUSED rather than the ENOENT/ECONNREFUSED Unix produces.
func platformUnavailableSocketError(err error) bool {
	return errors.Is(err, wsaeconnrefused) || errors.Is(err, wsaenetdown)
}
