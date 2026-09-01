package beads

import (
	"errors"
	"path/filepath"
)

// persistSQLiteSequenceFloorAtLeast serializes the final floor re-read and
// atomic replacement across processes. The lock target is the directory holding
// the store, which satisfies three constraints at once that no file in it can.
//
// graph.seqfloor is replaced by rename, so it cannot carry its own lock: two
// processes would end up holding locks on different inodes. A sibling lock file
// is also ruled out — the store directory is a censused namespace. Graph
// migration enumerates every entry in it and pins each one's presence, identity
// and bytes as a preservation fact, so an extra file there is not cosmetic
// clutter but a new term in the migration contract.
//
// The database inode is equally unusable, though only one platform says so.
// flock(2) locks belong to the open file description rather than the process,
// so locking the database contends with the store's own live connection instead
// of being re-entrant. Linux never shows it — SQLite serializes there with
// POSIX fcntl byte-range locks and never flocks the database — but macOS builds
// SQLite with SQLITE_ENABLE_LOCKING_STYLE and selects an flock-based VFS, so the
// store's open connection holds an flock on the database and a blocking LOCK_EX
// here deadlocked against it permanently (gas-bsj).
//
// The directory is left. It always exists, its inode is stable across the
// rename, SQLite never flocks it on either platform, and locking it creates
// nothing for the census to see. The cost is granularity: this flock is the
// store directory's single lock, owned by the sequence floor. Anything else
// needing to serialize on this directory must coordinate through here rather
// than take its own flock on the same inode.
//
// Windows cannot express any of that: it has no flock, and LockFileEx refuses a
// directory handle outright. lockSQLiteSequenceFloorDir is therefore a platform
// seam — a directory flock on Unix, a path-keyed named mutex on Windows, which
// is the one cross-process lock there that adds no entry to the censused
// namespace this comment is at pains to protect.
func persistSQLiteSequenceFloorAtLeast(floorPath string, requested int64) (persisted int64, returnErr error) {
	release, err := lockSQLiteSequenceFloorDir(filepath.Dir(floorPath))
	if err != nil {
		return 0, err
	}
	observeSQLiteSequenceFloorBoundary("sequence-floor-lock-held")
	defer func() {
		observeSQLiteSequenceFloorBoundary("sequence-floor-lock-release-before")
		if err := release(); err != nil {
			returnErr = errors.Join(returnErr, err)
		} else {
			observeSQLiteSequenceFloorBoundary("sequence-floor-lock-release-after")
		}
	}()

	current, err := readSQLiteSequenceFloor(floorPath)
	if err != nil {
		return 0, err
	}
	if current > requested {
		requested = current
	}
	if err := writeSQLiteSequenceFloor(floorPath, requested); err != nil {
		return 0, err
	}
	return requested, nil
}
