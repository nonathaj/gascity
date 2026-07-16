//go:build windows

package proctable

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gastownhall/gascity/internal/runtime"
)

// winRecord is one process-table row: identity, parentage, executable name,
// and (when readable) the process environment.
type winRecord struct {
	pid     int
	ppid    int
	command string
	env     map[string]string
}

// ScanBySessionID returns live agent root processes whose environment carries
// GC_SESSION_ID equal to id. Empty id returns all roots with any GC_SESSION_ID.
func ScanBySessionID(id string) ([]runtime.LiveRuntime, error) {
	if err := liveScanGuard(); err != nil {
		return []runtime.LiveRuntime{}, err
	}
	records, err := winRecords()
	if err != nil {
		return []runtime.LiveRuntime{}, err
	}
	var out []runtime.LiveRuntime
	for _, record := range records {
		if !winRecordIsSessionRoot(records, record, id) {
			continue
		}
		epoch, _ := strconv.Atoi(record.env["GC_RUNTIME_EPOCH"])
		city := record.env["GC_CITY_PATH"]
		if city == "" {
			city = record.env["GC_CITY"]
		}
		out = append(out, runtime.LiveRuntime{
			SessionID: record.env["GC_SESSION_ID"],
			City:      city,
			Epoch:     epoch,
			PID:       record.pid,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PID < out[j].PID
	})
	if out == nil {
		out = []runtime.LiveRuntime{}
	}
	return out, nil
}

// IsScanRoot reports whether pid is outside its GC_SESSION_ID parent's
// envelope and should be treated as an agent root.
func IsScanRoot(pid int) bool {
	if err := liveScanGuard(); err != nil {
		return false
	}
	if pid <= 0 || pid == os.Getpid() {
		return false
	}
	records, err := winRecords()
	if err != nil {
		return false
	}
	record, ok := records[pid]
	if !ok {
		return false
	}
	return winRecordIsSessionRoot(records, record, record.env["GC_SESSION_ID"])
}

// winRecordIsSessionRoot is the platform-independent record analysis: the
// record carries the wanted GC_SESSION_ID and its parent is not part of the
// same session envelope (mirrors the darwin ps-based scanner). Pure over the
// records map so it is unit-testable without a live process table.
func winRecordIsSessionRoot(records map[int]winRecord, record winRecord, id string) bool {
	if record.pid <= 4 { // 0=idle, 4=System
		return false
	}
	sessionID := record.env["GC_SESSION_ID"]
	if sessionID == "" {
		return false
	}
	if id != "" && sessionID != id {
		return false
	}
	if parent, ok := records[record.ppid]; ok &&
		parent.env["GC_SESSION_ID"] == sessionID &&
		!isInfrastructureCommand(parent.command) {
		return false
	}
	return true
}

// winRecords snapshots the process table via Toolhelp32 and attaches each
// same-user process's environment via the PEB read. Processes whose
// environment is unreadable (other users, system, WOW64, exited mid-scan)
// keep an empty env and are simply never session roots.
func winRecords() (map[int]winRecord, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("proctable: process snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // read-only handle

	records := make(map[int]winRecord)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("proctable: first process entry: %w", err)
	}
	for {
		pid := int(entry.ProcessID)
		record := winRecord{
			pid:     pid,
			ppid:    int(entry.ParentProcessID),
			command: strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])),
		}
		if env, err := readProcessEnv(pid); err == nil {
			record.env = env
		} else {
			record.env = map[string]string{}
		}
		records[pid] = record
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return records, nil
}
