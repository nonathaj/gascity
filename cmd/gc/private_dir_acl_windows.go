//go:build windows

package main

import (
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/winsec"
)

// internal/runtime is pinned to standard-library imports (RUNTIME-INV-001), so
// it cannot call winsec itself. Installing the hook here keeps that boundary
// intact while still giving every private directory the gc binary creates a
// real owner-only DACL on Windows, where os.Chmod cannot revoke access.
func init() {
	runtime.PrivateDirACL = winsec.RestrictToOwner
}
