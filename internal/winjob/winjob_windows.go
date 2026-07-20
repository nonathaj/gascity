//go:build windows

package winjob

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Limits declares the containment properties applied to a job at
// creation. Zero values mean "no limit" for quantities and "off" for
// flags, except breakaway: a job denies breakaway unless AllowBreakaway
// is set, matching the Windows default — DenyBreakaway in the design
// doc is therefore the zero value here.
type Limits struct {
	// KillOnClose terminates every member process when the last handle
	// to the job closes (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE). This is
	// the load-bearing containment property: a killed wrapper's handle
	// closes with it, taking the whole tree down.
	KillOnClose bool
	// AllowBreakaway permits children spawned with
	// CREATE_BREAKAWAY_FROM_JOB to escape the job. Leave false for
	// containment jobs: gc's startDetached tries breakaway first and
	// already falls back to a no-breakaway spawn when denied.
	AllowBreakaway bool
	// JobMemory caps the total committed memory of all member
	// processes, in bytes (JOB_OBJECT_LIMIT_JOB_MEMORY). 0 = no cap.
	JobMemory uint64
	// CPURateWeight applies weight-based CPU rate control (1-9,
	// default-weight is 5; the analogue of slice CPUWeight). 0 = off.
	CPURateWeight uint32
}

// Job is an open handle to a Windows Job Object. Closing it releases
// the handle; with KillOnClose set and no other handles open, closing
// kills every member process.
type Job struct {
	handle windows.Handle
	name   string
}

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procOpenJobObjectW     = kernel32.NewProc("OpenJobObjectW")
	procIsProcessInJob     = kernel32.NewProc("IsProcessInJob")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
)

const (
	jobObjectCPURateControlEnable      = 0x1
	jobObjectCPURateControlWeightBased = 0x2
	// JobObjectCpuRateControlInformation info class (not in x/sys).
	jobObjectCPURateControlInformationClass = 15
)

type jobObjectCPURateControlInformation struct {
	ControlFlags uint32
	Weight       uint32 // union arm used with WEIGHT_BASED
}

// Create opens or creates the named job (empty name = anonymous) and
// applies limits. Creating an existing name returns a new handle to the
// same job with the limits re-applied, so concurrent wrappers sharing a
// name compose: kill-on-close fires only when the LAST handle closes.
func Create(name string, limits Limits) (*Job, error) {
	var namePtr *uint16
	if name != "" {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, fmt.Errorf("winjob: encoding job name %q: %w", name, err)
		}
		namePtr = p
	}
	h, err := windows.CreateJobObject(nil, namePtr)
	if err != nil {
		return nil, fmt.Errorf("winjob: creating job %q: %w", name, err)
	}
	j := &Job{handle: h, name: name}
	if err := j.applyLimits(limits); err != nil {
		_ = j.Close()
		return nil, err
	}
	return j, nil
}

func (j *Job) applyLimits(limits Limits) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if limits.KillOnClose {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	if limits.AllowBreakaway {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	}
	if limits.JobMemory > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(limits.JobMemory)
	}
	if info.BasicLimitInformation.LimitFlags != 0 {
		if _, err := windows.SetInformationJobObject(
			j.handle,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			return fmt.Errorf("winjob: setting limits on job %q: %w", j.name, err)
		}
	}
	if limits.CPURateWeight > 0 {
		rate := jobObjectCPURateControlInformation{
			ControlFlags: jobObjectCPURateControlEnable | jobObjectCPURateControlWeightBased,
			Weight:       limits.CPURateWeight,
		}
		if _, err := windows.SetInformationJobObject(
			j.handle,
			jobObjectCPURateControlInformationClass,
			uintptr(unsafe.Pointer(&rate)),
			uint32(unsafe.Sizeof(rate)),
		); err != nil {
			return fmt.Errorf("winjob: setting CPU rate weight on job %q: %w", j.name, err)
		}
	}
	return nil
}

// AssignCurrent places the calling process (and all its future
// descendants) into the job.
func (j *Job) AssignCurrent() error {
	if err := windows.AssignProcessToJobObject(j.handle, windows.CurrentProcess()); err != nil {
		return fmt.Errorf("winjob: assigning current process to job %q: %w", j.name, err)
	}
	return nil
}

// Assign places pid into the job. Prefer assigning before the process
// runs (create-suspended → assign → resume) — a running process may
// already have spawned children that will NOT be pulled in.
func (j *Job) Assign(pid int) error {
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("winjob: opening PID %d: %w", pid, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // handle cleanup
	if err := windows.AssignProcessToJobObject(j.handle, h); err != nil {
		return fmt.Errorf("winjob: assigning PID %d to job %q: %w", pid, j.name, err)
	}
	return nil
}

// MemoryBudget returns the job's committed-memory cap and the peak
// usage observed so far (both 0 when no cap is set).
func (j *Job) MemoryBudget() (limit, peakUsed uint64, err error) {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(
		j.handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	); err != nil {
		return 0, 0, fmt.Errorf("winjob: querying job %q: %w", j.name, err)
	}
	return uint64(info.JobMemoryLimit), uint64(info.PeakJobMemoryUsed), nil
}

// Terminate kills every member process with the given exit code.
func (j *Job) Terminate(exitCode uint32) error {
	if err := windows.TerminateJobObject(j.handle, exitCode); err != nil {
		return fmt.Errorf("winjob: terminating job %q: %w", j.name, err)
	}
	return nil
}

// Close releases the handle. With KillOnClose and no other open
// handles, this kills all member processes.
func (j *Job) Close() error {
	if j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	if err != nil {
		return fmt.Errorf("winjob: closing job %q: %w", j.name, err)
	}
	return nil
}

const jobObjectQueryAccess = 0x0004 // JOB_OBJECT_QUERY

// InJob reports whether the current process is a member of the named
// job. A job that does not exist is reported as not-a-member.
func InJob(name string) (bool, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, fmt.Errorf("winjob: encoding job name %q: %w", name, err)
	}
	h, _, callErr := procOpenJobObjectW.Call(
		uintptr(jobObjectQueryAccess),
		0, // bInheritHandle = FALSE
		uintptr(unsafe.Pointer(namePtr)),
	)
	if h == 0 {
		if errno, ok := callErr.(windows.Errno); ok && errno == windows.ERROR_FILE_NOT_FOUND {
			return false, nil
		}
		return false, fmt.Errorf("winjob: opening job %q: %w", name, callErr)
	}
	defer windows.CloseHandle(windows.Handle(h)) //nolint:errcheck // handle cleanup
	var result int32
	r, _, callErr := procIsProcessInJob.Call(
		uintptr(windows.CurrentProcess()),
		h,
		uintptr(unsafe.Pointer(&result)),
	)
	if r == 0 {
		return false, fmt.Errorf("winjob: IsProcessInJob for job %q: %w", name, callErr)
	}
	return result != 0, nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// AvailablePhysicalMemory returns the host's currently available
// physical memory in bytes.
func AvailablePhysicalMemory() (uint64, error) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	r, _, callErr := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return 0, fmt.Errorf("winjob: GlobalMemoryStatusEx: %w", callErr)
	}
	return status.AvailPhys, nil
}
