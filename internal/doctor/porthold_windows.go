//go:build windows

package doctor

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// portHolderPIDPlatform resolves the PID listening on a local TCP port
// via GetExtendedTcpTable — the Windows analogue of the /proc/net/tcp
// inode walk and the lsof fallback, neither of which exists there.
// Returns (pid, true) when the lookup ran (pid 0 = no listener found).
func portHolderPIDPlatform(port int) (int, bool) {
	if pid := tcpListenerPID(windows.AF_INET, uint16(port)); pid > 0 {
		return pid, true
	}
	if pid := tcpListenerPID(windows.AF_INET6, uint16(port)); pid > 0 {
		return pid, true
	}
	return 0, true
}

var (
	iphlpapi                 = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable  = iphlpapi.NewProc("GetExtendedTcpTable")
	tcpTableOwnerPIDListener = uintptr(3) // TCP_TABLE_OWNER_PID_LISTENER
)

type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

type mibTCP6RowOwnerPID struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    uint32
	State         uint32
	OwningPID     uint32
}

// tcpListenerPID returns the owning PID of a LISTEN socket on port for
// the given address family, or 0.
func tcpListenerPID(family int, port uint16) int {
	var size uint32
	// First call sizes the buffer (ERROR_INSUFFICIENT_BUFFER expected).
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0) //nolint:errcheck // sizing call
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	r, _, _ := procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0, // unsorted
		uintptr(family),
		tcpTableOwnerPIDListener,
		0,
	)
	if r != 0 { // NO_ERROR
		return 0
	}
	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	base := unsafe.Pointer(&buf[4])
	for i := uint32(0); i < count; i++ {
		var rowPort uint32
		var pid uint32
		switch family {
		case windows.AF_INET:
			row := (*mibTCPRowOwnerPID)(unsafe.Pointer(uintptr(base) + uintptr(i)*unsafe.Sizeof(mibTCPRowOwnerPID{})))
			rowPort, pid = row.LocalPort, row.OwningPID
		case windows.AF_INET6:
			row := (*mibTCP6RowOwnerPID)(unsafe.Pointer(uintptr(base) + uintptr(i)*unsafe.Sizeof(mibTCP6RowOwnerPID{})))
			rowPort, pid = row.LocalPort, row.OwningPID
		default:
			return 0
		}
		// dwLocalPort carries the port in network byte order in its low
		// 16 bits.
		if uint16(rowPort>>8)&0xff|uint16(rowPort&0xff)<<8 == port {
			return int(pid)
		}
	}
	return 0
}
