//go:build windows

package pidutil

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// TCP listener attribution via GetExtendedTcpTable — the Windows
// analogue of the /proc/net/tcp inode walk and the lsof fallback,
// neither of which exists here. Shared by doctor's managed-dolt state
// validation and dolt process discovery.

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

type tcpListener struct {
	port uint16
	pid  int
}

// TCPListenerPID resolves the PID listening on a local TCP port.
// checked is true when the platform lookup ran (pid 0 = no listener).
func TCPListenerPID(port int) (pid int, checked bool) {
	if port <= 0 || port > 0xffff {
		return 0, true
	}
	for _, l := range tcpListeners() {
		if int(l.port) == port {
			return l.pid, true
		}
	}
	return 0, true
}

// TCPListenerPortsByPID returns every LISTEN port grouped by owning
// PID. checked is true when the platform lookup ran.
func TCPListenerPortsByPID() (map[int][]int, bool) {
	out := map[int][]int{}
	for _, l := range tcpListeners() {
		out[l.pid] = append(out[l.pid], int(l.port))
	}
	return out, true
}

func tcpListeners() []tcpListener {
	var out []tcpListener
	out = append(out, tcpListenersForFamily(windows.AF_INET)...)
	out = append(out, tcpListenersForFamily(windows.AF_INET6)...)
	return out
}

func tcpListenersForFamily(family int) []tcpListener {
	var size uint32
	// First call sizes the buffer (ERROR_INSUFFICIENT_BUFFER expected).
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0) //nolint:errcheck // sizing call
	if size == 0 {
		return nil
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
		return nil
	}
	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	base := unsafe.Pointer(&buf[4])
	out := make([]tcpListener, 0, count)
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
			return out
		}
		// dwLocalPort carries the port in network byte order in its low
		// 16 bits.
		out = append(out, tcpListener{
			port: uint16(rowPort>>8)&0xff | uint16(rowPort&0xff)<<8,
			pid:  int(pid),
		})
	}
	return out
}
