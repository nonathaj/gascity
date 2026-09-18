//go:build windows

package runtime

import "os"

// privateDirOwnedBy is a no-op on Windows. There is no numeric uid to compare:
// os.Geteuid reports -1 and ownership is expressed by the directory's DACL,
// which this stdlib-only package (TestRuntimeContractPackageStaysStdlibOnly)
// cannot inspect. The callers that harden credential-adjacent directories on
// Windows do so through internal/winsec at their own layer.
func privateDirOwnedBy(string, os.FileInfo, int) error { return nil }
