//go:build !windows

package main

// discoverDoltProcessesPlatform returns ok=false on Unix so the shared
// discoverDoltProcesses falls through to its /proc and `ps` enumeration paths,
// which already cover Linux and macOS.
func discoverDoltProcessesPlatform() ([]DoltProcInfo, bool, error) {
	return nil, false, nil
}
