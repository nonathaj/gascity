package fsys

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// RemoveAll removes path and any children using the provided filesystem.
// Missing paths are treated as already removed. Symlink paths are removed as
// links and are not followed.
func RemoveAll(fs FS, path string) error {
	info, err := fs.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return removeWithTransientRetry(fs, path)
	}

	entries, err := fs.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var removeErr error
	for _, entry := range entries {
		if err := RemoveAll(fs, filepath.Join(path, entry.Name())); err != nil {
			removeErr = errors.Join(removeErr, err)
		}
	}
	if removeErr != nil {
		return removeErr
	}
	return removeWithTransientRetry(fs, path)
}

// removeWithTransientRetry removes path, retrying briefly on the transient
// Windows sharing class (ERROR_ACCESS_DENIED / ERROR_SHARING_VIOLATION):
// antivirus, the search indexer, or a just-exited process's not-yet-released
// handle can hold a file or directory open for a few milliseconds, and NTFS
// refuses the delete while they do. Reuses the rename classifier — the same
// sharing errnos apply — so the retry is Windows-only in practice and a plain
// pass-through on Unix. A not-exist result is success.
func removeWithTransientRetry(fs FS, path string) error {
	delay := time.Millisecond
	for attempt := 0; ; attempt++ {
		err := fs.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		if attempt >= 8 || !isTransientRenameError(err) {
			return err
		}
		time.Sleep(delay)
		delay *= 2 // 1+2+...+128ms ≈ 255ms worst case
	}
}
