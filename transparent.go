package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

var echWarning sync.Once

// relay handles one redirected connection that was originally going to dst:
// it finds out which hostname the client is talking to and picks the tunnel
// or the direct route. A nil rule means the connection is not subject to any
// rule and goes out directly.
func relay(c net.Conn, tnet *netstack.Net, rule *Rule, dst netip.AddrPort) {
	target := dst.String()
	var peeked []byte
	useTunnel := rule != nil && len(rule.Domains) == 0
	if rule != nil && !useTunnel {
		var host string
		host, peeked = sniffHost(c)
		if host == "cloudflare-ech.com" {
			echWarning.Do(func() {
				log.Printf("WARNING: %s uses encrypted SNI (ECH), so domains cannot be seen. "+
					"Turn off 'Use secure DNS' in the browser.", rule.Exe)
			})
		}
		if debug {
			log.Printf("DEBUG  %s  %s  host=%q  first %d bytes", rule.Exe, target, host, len(peeked))
		}
		if useTunnel = rule.match(host); useTunnel {
			// let the tunnel's DNS resolve it
			target = net.JoinHostPort(host, strconv.Itoa(int(dst.Port())))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var up net.Conn
	var err error
	if useTunnel {
		log.Printf("TUNNEL  %s  %s", rule.Exe, target)
		up, err = tnet.DialContext(ctx, "tcp", target)
	} else {
		up, err = (&net.Dialer{}).DialContext(ctx, "tcp", target)
	}
	if err != nil {
		return
	}
	defer up.Close()

	if _, err := up.Write(peeked); err != nil {
		return
	}
	pipe(c, up)
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}

// sniffConn lets crypto/tls parse a ClientHello without answering it.
type sniffConn struct {
	net.Conn
	r io.Reader
}

func (s sniffConn) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s sniffConn) Write(p []byte) (int, error) { return len(p), nil }
func (s sniffConn) Close() error                { return nil }

// sniffHost reads the start of the client's stream and extracts the hostname
// from the TLS SNI or the HTTP Host header. It returns everything it consumed
// so the caller can replay it upstream.
func sniffHost(c net.Conn) (host string, peeked []byte) {
	var buf bytes.Buffer
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	defer c.SetReadDeadline(time.Time{})

	tee := io.TeeReader(c, &buf)
	first := make([]byte, 1)
	if _, err := io.ReadFull(tee, first); err != nil {
		return "", buf.Bytes()
	}
	rd := io.MultiReader(bytes.NewReader(first), tee)

	switch {
	case first[0] == 0x16: // TLS handshake record
		tls.Server(sniffConn{c, rd}, &tls.Config{
			GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
				host = h.ServerName
				return nil, errors.New("sniffed")
			},
		}).Handshake()
	case first[0] >= 'A' && first[0] <= 'Z': // HTTP method
		if req, err := http.ReadRequest(bufio.NewReader(rd)); err == nil {
			host = req.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
		}
	}
	return host, buf.Bytes()
}
