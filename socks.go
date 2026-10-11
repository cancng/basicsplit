package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

// proxy is a minimal SOCKS5 server (CONNECT only, no auth) that sends
// connections to matching domains through the tunnel and everything else
// out the normal way.
type proxy struct {
	tnet *netstack.Net
}

func (p *proxy) listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go p.handle(c)
	}
}

func (p *proxy) handle(c net.Conn) {
	defer c.Close()

	host, port, err := socksHandshake(c)
	if err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var up net.Conn
	if current.Load().matchDomain(host) {
		log.Printf("TUNNEL  %s", target)
		// hostname is resolved by the tunnel's DNS, so no DNS leak
		up, err = p.tnet.DialContext(ctx, "tcp", target)
	} else {
		up, err = (&net.Dialer{}).DialContext(ctx, "tcp", target)
	}
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	pipe(c, up)
}

func socksHandshake(c net.Conn) (host string, port int, err error) {
	buf := make([]byte, 262)

	// greeting: VER NMETHODS METHODS...
	if _, err = io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	if buf[0] != 5 {
		return "", 0, fmt.Errorf("not socks5")
	}
	if _, err = io.ReadFull(c, buf[:buf[1]]); err != nil {
		return
	}
	if _, err = c.Write([]byte{5, 0}); err != nil {
		return
	}

	// request: VER CMD RSV ATYP ADDR PORT
	if _, err = io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	if buf[1] != 1 {
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", 0, fmt.Errorf("only CONNECT is supported")
	}
	switch buf[3] {
	case 1:
		if _, err = io.ReadFull(c, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 3:
		if _, err = io.ReadFull(c, buf[:1]); err != nil {
			return
		}
		n := int(buf[0])
		if _, err = io.ReadFull(c, buf[:n]); err != nil {
			return
		}
		host = string(buf[:n])
	case 4:
		if _, err = io.ReadFull(c, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		return "", 0, fmt.Errorf("unknown address type")
	}
	if _, err = io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	port = int(binary.BigEndian.Uint16(buf[:2]))
	return
}
