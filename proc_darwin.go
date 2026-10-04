package main

import (
	"encoding/binary"
	"net/netip"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ownerPIDs returns the processes that own a TCP socket bound to the given
// local address. There can be more than one: a filtering network extension
// (AdGuard, for instance) makes the real connection itself, and the socket
// of the app it took over is then given the same address. The list is only
// complete for root; other users see just their own process's sockets.
//
// It walks net.inet.tcp.pcblist_n, where every socket is a run of records
// that each start with their own length and kind.
func ownerPIDs(local netip.AddrPort) []uint32 {
	const (
		kindSocket = 0x001 // xsocket_n
		kindInpcb  = 0x010 // xinpcb_n
	)
	// the fetch fails if the table grew after its size was queried
	var buf []byte
	for try := 0; try < 3; try++ {
		var err error
		if buf, err = unix.SysctlRaw("net.inet.tcp.pcblist_n"); err == nil {
			break
		}
	}
	if len(buf) < 4 {
		return nil
	}
	var pids []uint32
	le := binary.LittleEndian
	matched := false
	// the list starts with an xinpgen header, whose first field is its length
	for i := int(le.Uint32(buf)); i+8 <= len(buf); {
		size, kind := int(le.Uint32(buf[i:])), le.Uint32(buf[i+4:])
		if size < 8 || i+size > len(buf) {
			break
		}
		rec := buf[i : i+size]
		switch {
		case kind == kindInpcb && size >= 80:
			matched = inpcbIs(rec, local)
		case kind == kindSocket && size >= 72:
			if matched {
				pids = append(pids, le.Uint32(rec[68:])) // so_last_pid
			}
			matched = false
		}
		i += (size + 7) &^ 7
	}
	return pids
}

// inpcbIs reports whether an xinpcb_n record is bound to the given address.
func inpcbIs(rec []byte, local netip.AddrPort) bool {
	const vflagIPv4, vflagIPv6 = 1, 2
	if binary.BigEndian.Uint16(rec[18:]) != local.Port() { // inp_lport
		return false
	}
	vflag, laddr := rec[44], rec[64:80]
	if local.Addr().Is4() {
		// an IPv4 address sits in the last 4 bytes
		return vflag&vflagIPv4 != 0 && netip.AddrFrom4([4]byte(laddr[12:])) == local.Addr()
	}
	return vflag&vflagIPv6 != 0 && netip.AddrFrom16([16]byte(laddr)) == local.Addr()
}

// exePath returns the full path of a process's executable, or "".
func exePath(pid uint32) string {
	const (
		procInfoCallPIDInfo = 2
		procPIDPathInfo     = 11
	)
	buf := make([]byte, unix.PathMax)
	_, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, procInfoCallPIDInfo, uintptr(pid),
		procPIDPathInfo, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return ""
	}
	return unix.ByteSliceToString(buf)
}

// ruleForPath finds the rule for an executable. A rule's exe matches the
// executable's file name, or an app bundle anywhere in its path, so that
// "Google Chrome" also covers the helper processes inside Google Chrome.app.
func (c *Config) ruleForPath(path string) *Rule {
	if path == "" {
		return nil
	}
	path = strings.ToLower(path)
	for i := range c.Rules {
		name := strings.TrimSuffix(strings.ToLower(c.Rules[i].Exe), ".app")
		if name == "" {
			continue
		}
		if filepath.Base(path) == name || strings.Contains(path, "/"+name+".app/") {
			return &c.Rules[i]
		}
	}
	return nil
}

// isExtension reports whether an executable is a system or app extension,
// such as a content filter that connects on behalf of other apps.
func isExtension(path string) bool {
	return strings.Contains(path, ".systemextension/") || strings.Contains(path, ".appex/")
}

// ruleForConn finds the rule for the app behind a captured connection, and
// returns the owners it saw for logging.
func ruleForConn(src netip.AddrPort) (*Rule, []string) {
	deadline := time.Now().Add(time.Second)
	for {
		var owners []string
		proxied := false
		for _, pid := range ownerPIDs(src) {
			path := exePath(pid)
			if rule := current.Load().ruleForPath(path); rule != nil {
				return rule, append(owners, path)
			}
			owners = append(owners, path)
			proxied = proxied || isExtension(path)
		}
		// When an extension connects for an app, the app's own socket only
		// gets this address once the extension reports the connection as
		// open, so it can show up a moment after the connection reaches us.
		if !proxied || len(owners) > 1 || time.Now().After(deadline) {
			return nil, owners
		}
		time.Sleep(5 * time.Millisecond)
	}
}
