package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// exemptGID is a group ID nobody else uses; the process switches to it at
// startup. Exempting root instead would miss apps whose traffic is taken over
// by a filtering network extension, since those run as root.
const exemptGID = 4729

// The stock /etc/pf.conf evaluates every anchor under com.apple/, so rules
// loaded here take effect without editing the main ruleset.
const pfAnchor = "com.apple/basicsplit"

// redirector uses pf to send outbound TCP connections to a local listener,
// which looks up the owning process and decides per connection whether to go
// through the tunnel or out directly. pf cannot match on the process, so
// everyone's connections come through here. Only sockets of the group
// exemptGID are left alone, which keeps our own outgoing connections from
// looping back.
type redirector struct {
	tnet *netstack.Net
	pf   *os.File // /dev/pf, for looking up original destinations
	port uint16
	ipv6 bool

	mu      sync.Mutex
	loaded  string // rules currently in the anchor
	token   string // our reference on pf being enabled
	stopped bool
}

func startRedirector(tnet *netstack.Net) (*redirector, error) {
	pf, err := os.OpenFile("/dev/pf", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("/dev/pf acilamadi: %w", err)
	}
	if err := checkMainRuleset(); err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &redirector{tnet: tnet, pf: pf, port: uint16(ln.Addr().(*net.TCPAddr).Port)}
	go r.accept(ln)
	if ln6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", r.port)); err == nil {
		r.ipv6 = true
		go r.accept(ln6)
	} else {
		log.Printf("IPv6 yakalanmayacak: %v", err)
	}

	// rules left behind by a crash point at a port nobody listens on
	pfctl(nil, "-a", pfAnchor, "-F", "all")

	cleanup = r.stop
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		cleanup()
		os.Exit(0)
	}()
	return r, nil
}

// run keeps the pf rules in step with the config and the on/off switch.
func (r *redirector) run() error {
	for {
		rules := ""
		if enabled.Load() {
			rules = r.rules(current.Load())
		}
		if err := r.apply(rules); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
}

// forgetVerdicts has nothing to do here: run picks up rule changes itself.
func (r *redirector) forgetVerdicts() {}

func (r *redirector) rules(cfg *Config) string {
	if len(cfg.Rules) == 0 {
		return ""
	}
	// Hostnames are only visible on web ports, so unless some rule tunnels
	// an exe's whole traffic there is no need to capture anything else.
	ports := " port { 80, 443 }"
	for _, rule := range cfg.Rules {
		if len(rule.Domains) == 0 {
			ports = ""
		}
	}

	var b strings.Builder
	b.WriteString("table <local> const { 127.0.0.0/8, ::1, fe80::/10 }\n")
	fmt.Fprintf(&b, "rdr pass on lo0 inet proto tcp from any to ! <local>%s -> 127.0.0.1 port %d\n", ports, r.port)
	if r.ipv6 {
		fmt.Fprintf(&b, "rdr pass on lo0 inet6 proto tcp from any to ! <local>%s -> ::1 port %d\n", ports, r.port)
	}
	fmt.Fprintf(&b, "pass out route-to (lo0 127.0.0.1) inet proto tcp from any to ! <local>%s group != %d\n", ports, exemptGID)
	if r.ipv6 {
		fmt.Fprintf(&b, "pass out route-to (lo0 ::1) inet6 proto tcp from any to ! <local>%s group != %d\n", ports, exemptGID)
	}
	if !cfg.AllowQUIC {
		// QUIC hides the hostname; refuse it so browsers fall back to TCP
		fmt.Fprintf(&b, "block return out quick proto udp from any to any port 443 group != %d\n", exemptGID)
	}
	return b.String()
}

// apply makes the anchor contain exactly the given rules.
func (r *redirector) apply(rules string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || rules == r.loaded {
		return nil
	}
	if rules == "" {
		if _, err := pfctl(nil, "-a", pfAnchor, "-F", "all"); err != nil {
			return err
		}
		log.Print("pf kurallari kaldirildi")
	} else {
		if r.token == "" {
			out, err := pfctl(nil, "-E")
			if err != nil {
				return err
			}
			m := regexp.MustCompile(`Token : (\d+)`).FindSubmatch(out)
			if m == nil {
				return fmt.Errorf("pfctl -E: token yok: %s", out)
			}
			r.token = string(m[1])
		}
		if _, err := pfctl([]byte(rules), "-a", pfAnchor, "-f", "-"); err != nil {
			return err
		}
		log.Print("pf kurallari yuklendi")
	}
	r.loaded = rules
	return nil
}

// stop removes the rules for good. Connections that are already open keep
// working for as long as the process lives.
func (r *redirector) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.stopped = true
	pfctl(nil, "-a", pfAnchor, "-F", "all")
	if r.token != "" {
		pfctl(nil, "-X", r.token)
	}
}

func pfctl(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("/sbin/pfctl", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("pfctl %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}

// checkMainRuleset makes sure the main ruleset still refers to the com.apple
// anchors. Something else (another VPN, a custom pf.conf) may have replaced
// it, and overwriting that is not ours to do.
func checkMainRuleset() error {
	for _, c := range []struct{ show, want string }{
		{"nat", `rdr-anchor "com.apple/*"`},
		{"rules", `anchor "com.apple/*"`},
	} {
		out, err := pfctl(nil, "-s", c.show)
		if err != nil {
			return err
		}
		if !bytes.Contains(out, []byte(c.want)) {
			return fmt.Errorf("pf ana kural setinde %s yok; 'sudo pfctl -f /etc/pf.conf' ile varsayilani yukle", c.want)
		}
	}
	return nil
}

func (r *redirector) accept(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			fatal("accept: %v", err)
		}
		go r.serve(c)
	}
}

func (r *redirector) serve(c net.Conn) {
	defer c.Close()

	src := unzone(c.RemoteAddr())
	dst, err := r.origDst(src, unzone(c.LocalAddr()))
	if err != nil {
		return // not redirected by pf (e.g. a local program poking the port)
	}
	rule, owners := ruleForConn(src)
	if debug {
		log.Printf("DEBUG  %v -> %v  sahipler=%q  kural=%v", src, dst, owners, rule != nil)
	}
	relay(c, r.tnet, rule, dst)
}

func unzone(a net.Addr) netip.AddrPort {
	ap := a.(*net.TCPAddr).AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port())
}

// struct pfioc_natlook
type natlook struct {
	saddr, daddr, rsaddr, rdaddr       [16]byte
	sxport, dxport, rsxport, rdxport   [4]byte
	af, proto, protoVariant, direction uint8
}

// origDst asks pf where a redirected connection was going before the rdr
// rule rewrote its destination to our listener.
func (r *redirector) origDst(src, local netip.AddrPort) (netip.AddrPort, error) {
	const (
		// _IOWR('D', 23, struct pfioc_natlook)
		diocNatLook = 0xc0000000 | unsafe.Sizeof(natlook{})<<16 | 'D'<<8 | 23
		pfIn, pfOut = 1, 2
	)
	for _, dir := range []uint8{pfOut, pfIn} {
		nl := natlook{af: unix.AF_INET, proto: unix.IPPROTO_TCP, direction: dir}
		if src.Addr().Is6() {
			nl.af = unix.AF_INET6
		}
		copy(nl.saddr[:], src.Addr().AsSlice())
		copy(nl.daddr[:], local.Addr().AsSlice())
		nl.sxport[0], nl.sxport[1] = byte(src.Port()>>8), byte(src.Port())
		nl.dxport[0], nl.dxport[1] = byte(local.Port()>>8), byte(local.Port())
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, r.pf.Fd(), diocNatLook, uintptr(unsafe.Pointer(&nl))); errno != 0 {
			continue
		}
		var addr netip.Addr
		if nl.af == unix.AF_INET {
			addr = netip.AddrFrom4([4]byte(nl.rdaddr[:4]))
		} else {
			addr = netip.AddrFrom16(nl.rdaddr)
		}
		dst := netip.AddrPortFrom(addr, uint16(nl.rdxport[0])<<8|uint16(nl.rdxport[1]))
		if dst == local {
			break
		}
		return dst, nil
	}
	return netip.AddrPort{}, errors.New("pf state yok")
}
