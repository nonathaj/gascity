//go:build !windows

package doctor

// portHolderPIDPlatform reports no platform-specific port-holder lookup
// on Unix hosts; managedDoltDoctorPortHolderPID falls through to the
// /proc/net/tcp inode walk and the lsof fallback.
func portHolderPIDPlatform(int) (int, bool) {
	return 0, false
}
