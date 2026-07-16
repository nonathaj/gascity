//go:build windows

package events

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// statErrIsClosed reports whether err is the error a *os.File returns from
// Stat after the file has been closed. Unlike Unix (which surfaces
// os.ErrClosed), Windows fstat on a released handle fails the underlying
// GetFileType/GetFileInformationByHandle call with ERROR_INVALID_HANDLE, so
// accept that too as proof the descriptor was released rather than leaked.
func statErrIsClosed(err error) bool {
	return errors.Is(err, os.ErrClosed) || errors.Is(err, windows.ERROR_INVALID_HANDLE)
}
