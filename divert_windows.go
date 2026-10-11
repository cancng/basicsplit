package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

var (
	windivert         = windows.NewLazyDLL("WinDivert.dll")
	procDivertOpen    = windivert.NewProc("WinDivertOpen")
	procDivertRecv    = windivert.NewProc("WinDivertRecv")
	procDivertSend    = windivert.NewProc("WinDivertSend")
	procCalcChecksums = windivert.NewProc("WinDivertHelperCalcChecksums")
)

const (
	protoTCP = 6
	protoUDP = 17

	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpACK = 0x10

	// bit position of Outbound in WINDIVERT_ADDRESS flags (offset 8)
	addrOutboundBit = 17
)

// natEntry remembers where a redirected connection was originally going.
type natEntry struct {
	dstIP    netip.Addr
	dstPort  uint16
	rule     *Rule
	created  time.Time
	accepted bool
}

type udpVerdict struct {
	drop    bool
	expires time.Time
}

// redirector captures outbound TCP connections made by the configured exes
// and reflects them into a local listener, which then decides per connection
// whether to go through the tunnel or out directly. It is the same technique
// as WinDivert's streamdump example: swap the IP addresses and reinject the
// packet as inbound, so the listener sees the original destination IP as its
// peer.
type redirector struct {
	tnet      *netstack.Net
	handle    uintptr
	ln        net.Listener
	proxyPort uint16

	mu    sync.Mutex
	nat   map[uint16]*natEntry // keyed by the client's source port
	udp   map[uint16]udpVerdict
	procs procCache
}

func startRedirector(tnet *netstack.Net) (*redirector, error) {
	if err := windivert.Load(); err != nil {
		return nil, fmt.Errorf("could not load WinDivert.dll (it must be next to the exe): %w", err)
	}
	filter, _ := windows.BytePtrFromString("outbound and !loopback and (tcp or udp.DstPort == 443)")
	h, _, err := procDivertOpen.Call(uintptr(unsafe.Pointer(filter)), 0, 0, 0)
	if windows.Handle(h) == windows.InvalidHandle {
		if err == windows.ERROR_ACCESS_DENIED {
			return nil, fmt.Errorf("could not open WinDivert: run as administrator")
		}
		return nil, fmt.Errorf("could not open WinDivert (is WinDivert64.sys next to the exe?): %w", err)
	}

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, err
	}
	r := &redirector{
		tnet:      tnet,
		handle:    h,
		ln:        ln,
		proxyPort: uint16(ln.Addr().(*net.TCPAddr).Port),
		nat:       map[uint16]*natEntry{},
		udp:       map[uint16]udpVerdict{},
		procs:     procCache{},
	}
	go r.accept()
	return r, nil
}

// run is the packet loop. It blocks until WinDivert fails.
func (r *redirector) run() error {
	pkt := make([]byte, 0xFFFF+64)
	var addr [80]byte // WINDIVERT_ADDRESS
	for {
		var n uint32
		ok, _, err := procDivertRecv.Call(r.handle,
			uintptr(unsafe.Pointer(&pkt[0])), uintptr(len(pkt)),
			uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&addr[0])))
		if ok == 0 {
			return fmt.Errorf("WinDivertRecv: %w", err)
		}
		if !r.process(pkt[:n], &addr) {
			continue
		}
		procDivertSend.Call(r.handle,
			uintptr(unsafe.Pointer(&pkt[0])), uintptr(n),
			0, uintptr(unsafe.Pointer(&addr[0])))
	}
}

// process rewrites p in place if it belongs to a redirected connection.
// It returns false if the packet should be dropped.
func (r *redirector) process(p []byte, addr *[80]byte) bool {
	var proto byte
	var addrOff, addrLen, l4 int
	switch {
	case len(p) >= 20 && p[0]>>4 == 4:
		if binary.BigEndian.Uint16(p[6:8])&0x3fff != 0 {
			return true // fragment
		}
		proto, addrOff, addrLen, l4 = p[9], 12, 4, int(p[0]&0x0f)*4
	case len(p) >= 40 && p[0]>>4 == 6:
		proto, addrOff, addrLen, l4 = p[6], 8, 16, 40
	default:
		return true
	}
	if proto != protoTCP && proto != protoUDP {
		return true
	}
	if len(p) < l4+8 || (proto == protoTCP && len(p) < l4+20) {
		return true
	}
	src := p[addrOff : addrOff+addrLen]
	dst := p[addrOff+addrLen : addrOff+2*addrLen]
	sport := binary.BigEndian.Uint16(p[l4:])
	dport := binary.BigEndian.Uint16(p[l4+2:])

	r.mu.Lock()
	defer r.mu.Unlock()

	if proto == protoUDP {
		// QUIC hides the hostname, so drop it for target exes; the browser
		// falls back to TCP.
		return !enabled.Load() || !r.dropUDP(sport)
	}

	if sport == r.proxyPort {
		// reply from our listener: make it look like it came from the
		// original destination
		e := r.nat[dport]
		if e == nil {
			return true
		}
		binary.BigEndian.PutUint16(p[l4:], e.dstPort)
	} else {
		e := r.nat[sport]
		dstIP, _ := netip.AddrFromSlice(dst)
		syn := p[l4+13]&(tcpSYN|tcpACK) == tcpSYN
		if syn && (e == nil || e.dstIP != dstIP || e.dstPort != dport) {
			delete(r.nat, sport)
			if !enabled.Load() {
				return true
			}
			rule := r.ruleForPort(tcpTable, sport)
			if rule == nil {
				return true
			}
			r.purgeStale()
			e = &natEntry{dstIP: dstIP, dstPort: dport, rule: rule, created: time.Now()}
			r.nat[sport] = e
		}
		if e == nil {
			return true
		}
		binary.BigEndian.PutUint16(p[l4+2:], r.proxyPort)
	}

	for i := range src {
		src[i], dst[i] = dst[i], src[i]
	}
	flags := binary.LittleEndian.Uint32(addr[8:12])
	binary.LittleEndian.PutUint32(addr[8:12], flags&^(1<<addrOutboundBit))
	procCalcChecksums.Call(uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)),
		uintptr(unsafe.Pointer(&addr[0])), 0)
	return true
}

// ruleForPort returns the rule for the exe that owns the given local port,
// or nil if that exe is not configured.
func (r *redirector) ruleForPort(t *portTable, port uint16) *Rule {
	for try := 0; ; try++ {
		if pid, ok := t.ownerPID(port); ok {
			return current.Load().ruleForExe(r.procs.exeName(pid))
		}
		if try == 2 {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (r *redirector) dropUDP(sport uint16) bool {
	now := time.Now()
	v, ok := r.udp[sport]
	if !ok || now.After(v.expires) {
		if len(r.udp) > 4096 {
			clear(r.udp)
		}
		v = udpVerdict{drop: r.ruleForPort(udpTable, sport) != nil, expires: now.Add(10 * time.Second)}
		r.udp[sport] = v
	}
	return v.drop
}

// forgetVerdicts makes rule changes apply to already-seen UDP ports.
func (r *redirector) forgetVerdicts() {
	r.mu.Lock()
	clear(r.udp)
	r.mu.Unlock()
}

// purgeStale removes entries whose connection never reached the listener.
func (r *redirector) purgeStale() {
	for port, e := range r.nat {
		if !e.accepted && time.Since(e.created) > time.Minute {
			delete(r.nat, port)
		}
	}
}

func (r *redirector) accept() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			fatal("accept: %v", err)
		}
		go r.serve(c)
	}
}

// serve hands one redirected connection to relay, with the destination it
// was originally going to.
func (r *redirector) serve(c net.Conn) {
	defer c.Close()

	peer := c.RemoteAddr().(*net.TCPAddr).AddrPort()
	r.mu.Lock()
	e := r.nat[peer.Port()]
	if e != nil && e.dstIP == peer.Addr().Unmap() {
		e.accepted = true
	} else {
		e = nil
	}
	r.mu.Unlock()
	if e == nil {
		return // not one of ours (e.g. someone on the LAN poking the port)
	}
	// keep the entry briefly so the closing packets still get rewritten
	defer time.AfterFunc(10*time.Second, func() {
		r.mu.Lock()
		if r.nat[peer.Port()] == e {
			delete(r.nat, peer.Port())
		}
		r.mu.Unlock()
	})

	relay(c, r.tnet, e.rule, netip.AddrPortFrom(e.dstIP, e.dstPort))
}
