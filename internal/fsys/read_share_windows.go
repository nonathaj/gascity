//go:build windows

package fsys

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readFileSharing reads name with a share mode that permits a concurrent rename or
// delete of the same file.
//
// This is what makes WriteFileAtomic actually atomic on Windows. os.Open requests
// FILE_SHARE_READ|FILE_SHARE_WRITE but NOT FILE_SHARE_DELETE, and NTFS refuses to
// replace a file while any handle lacking share-delete is open on it. So an ordinary
// reader does not merely risk a torn read — it BLOCKS the writer's rename outright,
// and a steady stream of readers can starve a replace indefinitely (observed:
// "Access is denied" from rename after the retry budget expired, while readers
// hammered the same path).
//
// POSIX has no equivalent constraint: a rename there is atomic with respect to
// readers, and an open reader keeps its inode regardless. Requesting share-delete
// here is therefore not a Windows quirk being papered over — it is how you obtain
// the semantics the rest of the codebase already assumes (doctrine: Tier 1
// mechanisms are Windows-NATIVE rather than emulations of POSIX behaviour).
//
// A reader that opened before the swap continues to see the old contents through its
// existing handle, which is exactly the POSIX guarantee.
func readFileSharing(name string) ([]byte, error) {
	pathp, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	handle, err := windows.CreateFile(
		pathp,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	file := os.NewFile(uintptr(handle), name)
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: name, Err: err}
	}
	return data, nil
}
