package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const controlSocket = "/var/run/basicsplit.sock"

var (
	started sync.Once
	running atomic.Bool
	// failure is the first fatal error, if there was one
	failure atomic.Pointer[string]
)

type status struct {
	// idle, starting, running or failed
	State   string `json:"state"`
	Enabled bool   `json:"enabled"`
	Error   string `json:"error,omitempty"`
}

func currentStatus() status {
	s := status{State: "idle", Enabled: enabled.Load()}
	switch msg := failure.Load(); {
	case msg != nil:
		s.State, s.Error = "failed", *msg
	case tunnelUp.Load():
		s.State = "running"
	case running.Load():
		s.State = "starting"
	}
	return s
}

// serveControl answers the app. Each connection carries one command and gets
// the resulting status back as a line of JSON:
//
//	status  just report
//	start   read the config and bring the tunnel and the capture up
//	stop    take everything down again
//	on/off  resume or pause capturing new connections
//
// The engine waits idle until it is started. Stopping it means exiting:
// launchd starts a fresh, idle one right away, which is simpler and safer
// than unwinding the tunnel in place.
func serveControl() error {
	os.Remove(controlSocket)
	ln, err := net.Listen("unix", controlSocket)
	if err != nil {
		return err
	}
	os.Chown(controlSocket, ownerUID, -1)
	os.Chmod(controlSocket, 0o600)
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
			case "start":
				started.Do(start)
			case "on":
				enabled.Store(true)
			case "off":
				enabled.Store(false)
			}
			json.NewEncoder(c).Encode(currentStatus())
			if cmd == "stop" && running.Load() {
				log.Print("stopped")
				cleanup()
				os.Exit(0)
			}
		}()
	}
}

func start() {
	running.Store(true)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		msg := "Could not read the config: " + err.Error()
		log.Print(msg)
		failure.Store(&msg)
		return
	}
	current.Store(cfg)
	enabled.Store(true)
	go run()
}
