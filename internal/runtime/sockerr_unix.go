//go:build !windows

package runtime

// platformUnavailableSocketError reports platform-specific "session is dead"
// dial errors beyond the portable set. Unix has none.
func platformUnavailableSocketError(error) bool { return false }
