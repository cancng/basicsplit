package main

import (
	"encoding/binary"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var iphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

type rowLayout struct{ af, size, portOff, pidOff int }

// portTable wraps GetExtendedTcpTable / GetExtendedUdpTable, which map local
// ports to owning process IDs.
type portTable struct {
	proc  *windows.LazyProc
	class uintptr
	rows  []rowLayout
}

var (
	// TCP_TABLE_OWNER_PID_ALL; MIB_TCPROW_OWNER_PID / MIB_TCP6ROW_OWNER_PID
	tcpTable = &portTable{iphlpapi.NewProc("GetExtendedTcpTable"), 5, []rowLayout{
		{windows.AF_INET, 24, 8, 20},
		{windows.AF_INET6, 56, 20, 52},
	}}
	// UDP_TABLE_OWNER_PID; MIB_UDPROW_OWNER_PID / MIB_UDP6ROW_OWNER_PID
	udpTable = &portTable{iphlpapi.NewProc("GetExtendedUdpTable"), 1, []rowLayout{
		{windows.AF_INET, 12, 4, 8},
		{windows.AF_INET6, 28, 20, 24},
	}}
)

// ownerPID looks the port up in both the IPv4 and IPv6 tables, since a
// dual-stack socket can show up in either.
func (t *portTable) ownerPID(port uint16) (uint32, bool) {
	for _, l := range t.rows {
		size := uint32(1 << 16)
		var buf []byte
		for {
			buf = make([]byte, size)
			ret, _, _ := t.proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)),
				0, uintptr(l.af), t.class, 0)
			if windows.Errno(ret) == windows.ERROR_INSUFFICIENT_BUFFER {
				continue
			}
			if ret != 0 {
				buf = nil
			}
			break
		}
		if len(buf) < 4 {
			continue
		}
		n := int(binary.LittleEndian.Uint32(buf))
		for i := 0; i < n && 4+(i+1)*l.size <= len(buf); i++ {
			row := buf[4+i*l.size:]
			// ports are stored in network byte order
			if binary.BigEndian.Uint16(row[l.portOff:]) == port {
				return binary.LittleEndian.Uint32(row[l.pidOff:]), true
			}
		}
	}
	return 0, false
}

type procName struct {
	name    string
	expires time.Time
}

// procCache maps PIDs to lowercase exe file names. Entries expire because
// PIDs get reused.
type procCache map[uint32]procName

func (c procCache) exeName(pid uint32) string {
	now := time.Now()
	if p, ok := c[pid]; ok && now.Before(p.expires) {
		return p.name
	}
	var name string
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil {
			name = strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
		}
		windows.CloseHandle(h)
	}
	c[pid] = procName{name, now.Add(30 * time.Second)}
	return name
}
