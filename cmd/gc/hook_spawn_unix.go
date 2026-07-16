//go:build !windows

package main

import "syscall"

// hookChildSysProcAttr is a no-op on Unix: shell-hook children need no special
// creation attributes, and adding process-group isolation here would change
// timeout/cancellation semantics for the reconciler's synchronous hooks. The
// hidden-console handling exists only to avoid the Windows desktop-heap
// exhaustion that its counterpart addresses.
func hookChildSysProcAttr() *syscall.SysProcAttr {
	return nil
}
