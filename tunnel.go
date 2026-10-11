package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// errResolve means the peer endpoint's hostname could not be looked up,
// which is usually temporary.
var errResolve = errors.New("could not resolve the Endpoint")

// startTunnel reads a standard WireGuard .conf file and brings up a userspace
// tunnel. No network adapter is created and no system routes are touched.
func startTunnel(confPath string) (*netstack.Net, error) {
	f, err := os.Open(confPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		addrs, dns []netip.Addr
		mtu        = 1420
		uapi       strings.Builder
		section    string
	)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)

		switch section + "." + key {
		case "interface.privatekey":
			k, err := keyToHex(val)
			if err != nil {
				return nil, fmt.Errorf("PrivateKey: %w", err)
			}
			fmt.Fprintf(&uapi, "private_key=%s\n", k)
		case "interface.address":
			for _, s := range splitList(val) {
				s, _, _ = strings.Cut(s, "/")
				a, err := netip.ParseAddr(s)
				if err != nil {
					return nil, fmt.Errorf("Address: %w", err)
				}
				addrs = append(addrs, a)
			}
		case "interface.dns":
			for _, s := range splitList(val) {
				// non-IP entries are search domains; skip them
				if a, err := netip.ParseAddr(s); err == nil {
					dns = append(dns, a)
				}
			}
		case "interface.mtu":
			if mtu, err = strconv.Atoi(val); err != nil {
				return nil, fmt.Errorf("MTU: %w", err)
			}
		case "peer.publickey":
			k, err := keyToHex(val)
			if err != nil {
				return nil, fmt.Errorf("PublicKey: %w", err)
			}
			fmt.Fprintf(&uapi, "public_key=%s\n", k)
		case "peer.presharedkey":
			k, err := keyToHex(val)
			if err != nil {
				return nil, fmt.Errorf("PresharedKey: %w", err)
			}
			fmt.Fprintf(&uapi, "preshared_key=%s\n", k)
		case "peer.endpoint":
			ep, err := net.ResolveUDPAddr("udp", val)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", errResolve, err)
			}
			fmt.Fprintf(&uapi, "endpoint=%s\n", ep.String())
		case "peer.allowedips":
			for _, s := range splitList(val) {
				fmt.Fprintf(&uapi, "allowed_ip=%s\n", s)
			}
		case "peer.persistentkeepalive":
			fmt.Fprintf(&uapi, "persistent_keepalive_interval=%s\n", val)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s has no [Interface] Address", confPath)
	}
	if len(dns) == 0 {
		dns = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	}

	tdev, tnet, err := netstack.CreateNetTUN(addrs, dns, mtu)
	if err != nil {
		return nil, err
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wg: "))
	if err := dev.IpcSet(uapi.String()); err != nil {
		return nil, err
	}
	if err := dev.Up(); err != nil {
		return nil, err
	}
	return tnet, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func keyToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("a key must be 32 bytes")
	}
	return hex.EncodeToString(raw), nil
}
