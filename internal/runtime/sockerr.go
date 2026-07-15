package runtime

import (
	"errors"
	"os"
	"syscall"
)

// IsUnavailableSocketError reports whether err from dialing a provider
// control socket means "session is dead" (socket missing or nothing
// listening) as opposed to a real failure. Providers treat these as
// idempotent-success for Stop and as not-running for liveness probes.
func IsUnavailableSocketError(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		platformUnavailableSocketError(err)
}
