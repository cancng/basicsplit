package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// The record offsets in ownerPIDs are not a stable ABI, so check them against
// a socket of our own.
func TestOwnerPID(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Logf("%s: %v", addr, err)
			continue
		}
		defer ln.Close()
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()

		local := c.LocalAddr().(*net.TCPAddr).AddrPort()
		pids := ownerPIDs(local)
		if len(pids) != 1 || pids[0] != uint32(os.Getpid()) {
			t.Fatalf("ownerPIDs(%v) = %v; want [%d]", local, pids, os.Getpid())
		}
		pid := pids[0]
		exe, _ := os.Executable()
		exe, _ = filepath.EvalSymlinks(exe)
		if got := exePath(pid); got != exe {
			t.Fatalf("exePath = %q, want %q", got, exe)
		}
	}
}

func TestRuleForPath(t *testing.T) {
	cfg := &Config{Rules: []Rule{{Exe: "Google Chrome"}, {Exe: "curl"}}}
	for path, want := range map[string]string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": "Google Chrome",
		"/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper": "Google Chrome",
		"/usr/bin/curl":   "curl",
		"/usr/bin/ssh":    "",
		"/opt/curl/bin/x": "",
	} {
		got := ""
		if r := cfg.ruleForPath(path); r != nil {
			got = r.Exe
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
}
