package testutil

// failer matches the *testing.T surface the fixture helpers need
// without importing the testing package into non-test files (same
// pattern as the watchdog's runner and CanonicalTempDir's tempDirer).
type failer interface {
	Helper()
	Cleanup(func())
	Fatalf(format string, args ...any)
	Skip(args ...any)
}
