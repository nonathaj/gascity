//go:build !windows

package events

import "os"

// openEventLog opens the append-mode event log. On Unix a regular O_APPEND
// handle already permits rename and unlink of the open file.
func openEventLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
}
