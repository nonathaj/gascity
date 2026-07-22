package fsys

import (
	"os"
	"time"
)

// RemoveDirIfEmpty removes an empty directory, tolerating a directory that is
// (or remains) non-empty because other live entries still occupy it. A missing
// directory counts as already removed.
//
// On Windows a just-closed handle can leave the directory's last child in the
// "delete-pending" state: the child's directory entry lingers for a few
// milliseconds after its handle is dropped, so the directory reads as
// non-empty even though nothing usable remains. The removal is retried through
// that window before a persistently non-empty directory is tolerated as
// success — its entries belong to another live user, such as a sibling
// service's socket. POSIX has no delete-pending state, so a non-empty
// directory is genuinely shared and is tolerated immediately without retrying.
// Any other failure is returned wrapped by the caller.
func RemoveDirIfEmpty(path string) error {
	delay := time.Millisecond
	for attempt := 0; ; attempt++ {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		if attempt >= 8 || !isRetryableDirRemoveError(err) {
			if isDirNotEmpty(err) {
				return nil // shared with other live entries; leave it in place
			}
			return err
		}
		time.Sleep(delay)
		delay *= 2 // 1+2+...+128ms ≈ 255ms worst case
	}
}
