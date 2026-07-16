//go:build windows

package fsys

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientRenameError reports whether a rename failure is the transient
// Windows sharing class: ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION,
// raised while an antivirus scanner, the search indexer, or a concurrent
// reader briefly holds the destination open. These clear within
// milliseconds and are safe to retry; anything else is final.
func isTransientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
