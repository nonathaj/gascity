//go:build windows

package dolttest

import "syscall"

// The dolt sql-server reaper discovers its targets exclusively through /proc
// (scanDoltSQLServers returns nil off Linux), so on Windows these process
// primitives are never reached with a real pid. They exist only to satisfy the
// cross-platform build: syscall.Kill has no Windows definition. If a Windows
// dolt reaper is ever needed it can grow real bodies here (e.g. via
// golang.org/x/sys/windows OpenProcess/TerminateProcess).
func signalPID(int, syscall.Signal) error { return nil }

func processExists(int) bool { return false }

func reraiseSignal(syscall.Signal) {}
