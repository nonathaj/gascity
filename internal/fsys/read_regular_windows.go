//go:build windows

package fsys

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// ReadRegularFile reads name without following a final symlink.
func (OSFS) ReadRegularFile(name string) ([]byte, error) {
	snapshot, err := (OSFS{}).readRegularFileSnapshot(name)
	if err != nil {
		return nil, err
	}
	return snapshot.data, nil
}

// readRegularFileSnapshot reads name without following a final reparse
// point (the Windows spelling of O_NOFOLLOW is FILE_FLAG_OPEN_REPARSE_POINT:
// the handle opens the link itself, which the reparse-attribute check below
// then rejects) and returns the opened file identity — volume serial plus
// 64-bit file index, the NTFS analogue of dev/ino — for post-read stability
// checks.
func (OSFS) readRegularFileSnapshot(name string) (regularFileSnapshot, error) {
	pathp, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return regularFileSnapshot{}, &os.PathError{Op: "open", Path: name, Err: err}
	}
	h, err := windows.CreateFile(pathp, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return regularFileSnapshot{}, &os.PathError{Op: "open", Path: name, Err: err}
	}

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return regularFileSnapshot{}, &os.PathError{Op: "stat", Path: name, Err: err}
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(h)
		return regularFileSnapshot{}, &os.PathError{Op: "open", Path: name, Err: os.ErrInvalid}
	}

	file := os.NewFile(uintptr(h), name)
	if file == nil {
		_ = windows.CloseHandle(h)
		return regularFileSnapshot{}, &os.PathError{Op: "open", Path: name, Err: os.ErrInvalid}
	}
	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return regularFileSnapshot{}, &os.PathError{Op: "read", Path: name, Err: err}
	}
	return regularFileSnapshot{
		data: data,
		id: fileIdentity{
			dev: uint64(info.VolumeSerialNumber),
			ino: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
		},
		hasID: true,
	}, nil
}
