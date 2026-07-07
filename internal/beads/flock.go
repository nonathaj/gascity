package beads

// Locker abstracts file-level locking for cross-process synchronization.
// FileStore uses it to serialize concurrent writers (CLI + controller).
type Locker interface {
	// Lock acquires an exclusive lock, blocking until available.
	Lock() error
	// Unlock releases the lock.
	Unlock() error
}

// nopLocker is a no-op Locker for use when file locking is not needed
// (e.g., tests with in-memory filesystems).
type nopLocker struct{}

func (nopLocker) Lock() error   { return nil }
func (nopLocker) Unlock() error { return nil }
