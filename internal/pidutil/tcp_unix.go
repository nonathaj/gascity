//go:build !windows

package pidutil

// TCPListenerPID reports no platform-specific port-holder lookup on
// Unix hosts; callers fall through to their /proc or lsof paths.
func TCPListenerPID(int) (int, bool) {
	return 0, false
}

// TCPListenerPortsByPID reports no platform-specific listener table on
// Unix hosts; callers fall through to their /proc or lsof paths.
func TCPListenerPortsByPID() (map[int][]int, bool) {
	return nil, false
}
