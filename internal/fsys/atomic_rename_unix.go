//go:build !windows

package fsys

// isTransientRenameError reports whether a rename failure is worth retrying.
// The transient class (a scanner or reader briefly holding the destination
// open) is a Windows NTFS behavior; POSIX rename replaces open files, so on
// Unix every failure is treated as final.
func isTransientRenameError(error) bool {
	return false
}
