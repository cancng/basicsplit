package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Any local user may connect; the commands only switch capturing on and off
// or stop the engine.
const controlSocket = "/var/run/basicsplit.sock"

// serveControl answers the tray process. Each connection carries one command
// (status, on, off or quit) and gets the resulting state back.
func serveControl() error {
	os.Remove(controlSocket)
	ln, err := net.Listen("unix", controlSocket)
	if err != nil {
		return err
	}
	os.Chmod(controlSocket, 0o666)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			c.SetDeadline(time.Now().Add(2 * time.Second))
			line, _ := bufio.NewReader(c).ReadString('\n')
			cmd := strings.TrimSpace(line)
			switch cmd {
			case "on":
				enabled.Store(true)
			case "off":
				enabled.Store(false)
			}
			fmt.Fprintf(c, "%t %t\n", tunnelUp.Load(), enabled.Load())
			if cmd == "quit" {
				cleanup()
				os.Remove(controlSocket)
				os.Exit(0)
			}
		}()
	}
}

// control sends a command to the engine and takes over the state it reports.
func control(cmd string) error {
	c, err := net.DialTimeout("unix", controlSocket, time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(c, cmd); err != nil {
		return err
	}
	var up, on bool
	if _, err := fmt.Fscan(c, &up, &on); err != nil {
		return err
	}
	tunnelUp.Store(up)
	enabled.Store(on)
	setTrayState()
	return nil
}
