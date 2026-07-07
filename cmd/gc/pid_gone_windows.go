//go:build windows

package main

import "github.com/gastownhall/gascity/internal/pidutil"

// pidGone reports whether the process at pid no longer exists. Windows has no
// zombie state, so a liveness probe is sufficient.
func pidGone(pid int) bool {
	return !pidutil.Alive(pid)
}
