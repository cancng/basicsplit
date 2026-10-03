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
)

var echWarning sync.Once

// serve handles one redirected connection: it finds out which hostname the
// client is talking to and picks the tunnel or the direct route.
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

	target := netip.AddrPortFrom(e.dstIP, e.dstPort).String()
	var peeked []byte
	useTunnel := len(e.rule.Domains) == 0
	if !useTunnel {
		var host string
		host, peeked = sniffHost(c)
		if host == "cloudflare-ech.com" {
			echWarning.Do(func() {
				log.Printf("UYARI: %s sifreli SNI (ECH) kullaniyor, alan adlari gorulemiyor. "+
					"Tarayicida 'Guvenli DNS kullan' ayarini kapat.", e.rule.Exe)
			})
		}
		if useTunnel = e.rule.match(host); useTunnel {
			// let the tunnel's DNS resolve it
			target = net.JoinHostPort(host, strconv.Itoa(int(e.dstPort)))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var up net.Conn
	var err error
	if useTunnel {
		log.Printf("TUNEL  %s  %s", e.rule.Exe, target)
		up, err = r.tnet.DialContext(ctx, "tcp", target)
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
