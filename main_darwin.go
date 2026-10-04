package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/unix"
)

// The program runs as two processes: the engine, as root, owns the tunnel
// and the pf rules; the tray icon runs as the logged-in user, since a root
// process has no business in the menu bar, and talks to the engine over a
// socket.
var engineMode = flag.Bool("engine", false, "root motoru olarak calis (tepsi bunu kendisi baslatir)")

func showError(msg string) { dialog(msg, "stop") }
func showInfo(msg string)  { dialog(msg, "note") }

func dialog(msg, icon string) {
	if *engineMode {
		fmt.Fprintln(os.Stderr, msg) // nobody to show it to; it is in the log
		return
	}
	exec.Command("/usr/bin/osascript", "-e", fmt.Sprintf(
		`display dialog %s with title "basicsplit" buttons {"Tamam"} default button 1 with icon %s`,
		appleQuote(msg), icon)).Run()
}

func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// adminRun runs a shell command as root, after macOS asks for an
// administrator password.
func adminRun(script string) error {
	out, err := exec.Command("/usr/bin/osascript", "-e",
		"do shell script "+appleQuote(script)+" with administrator privileges").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// elevate returns only in the engine. Otherwise this is the tray process,
// which starts the engine as root and never returns.
func elevate(exe string) {
	if !*engineMode {
		frontend(exe)
		os.Exit(0)
	}
	if os.Geteuid() != 0 {
		fatal("-engine root olarak calismali")
	}
	// the pf rules leave this group's sockets alone
	if err := unix.Setegid(exemptGID); err != nil {
		fatal("setegid: %v", err)
	}
}

var lockFile *os.File

// lock reports false if another process holds the lock file.
func lock(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return true
	}
	lockFile = f // the lock lives as long as the file stays open
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
}

// singleInstance reports false if another engine is already running.
func singleInstance() bool {
	return lock("/var/run/basicsplit.lock")
}

// runUI is the engine's side of the socket; the icon is in the tray process.
func runUI(logPath string) {
	fatal("kontrol soketi: %v", serveControl())
}

func frontend(exe string) {
	// catch the usual mistake here, where it can be shown
	if _, err := loadConfig(cfgPath); err != nil {
		fatal("Config okunamadi: %v", err)
	}
	if !lock(filepath.Join(os.TempDir(), "basicsplit-tray.lock")) {
		showInfo("basicsplit zaten calisiyor (menu cubuguna bak).")
		return
	}

	if !pollEngine() {
		cmd := shellQuote(exe) + " -engine -config " + shellQuote(cfgPath)
		if debug {
			cmd += " -debug"
		}
		// without the redirect osascript would wait for the engine to exit
		if err := adminRun(cmd + " >/dev/null 2>&1 &"); err != nil {
			if strings.Contains(err.Error(), "-128") {
				return // the password prompt was cancelled
			}
			fatal("Motor baslatilamadi: %v", err)
		}
		for i := 0; !pollEngine(); i++ {
			if i == 50 {
				fatal("Motor baslamadi. Ayrintilar: %s", logPath)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	go func() {
		for misses := 0; misses < 3; {
			time.Sleep(2 * time.Second)
			if pollEngine() {
				misses = 0
			} else {
				misses++
			}
		}
		if !quitting.Load() {
			showError("basicsplit motoru durdu. Ayrintilar: " + logPath)
		}
		os.Exit(1)
	}()
	systray.Run(func() { onReady(logPath) }, nil)
}

// pollEngine copies the engine's state into this process for the tray icon.
// It reports false if the engine does not answer.
func pollEngine() bool {
	return control("status") == nil
}

func setEnabled(on bool) {
	cmd := "off"
	if on {
		cmd = "on"
	}
	control(cmd)
}

func stopEngine() {
	control("quit")
}
