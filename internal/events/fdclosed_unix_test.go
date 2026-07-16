//go:build !windows

package events

import (
	"errors"
	"os"
)

// statErrIsClosed reports whether err is the error a *os.File returns from
// Stat after the file has been closed. On Unix the os package surfaces this
// as os.ErrClosed.
func statErrIsClosed(err error) bool {
	return errors.Is(err, os.ErrClosed)
}
