package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"
)

// On macOS this binary is only the engine: a root daemon that owns the
// tunnel and the pf rules. The app in macos/ installs it, drives it over a
// socket, and also runs it unprivileged to read and write the config file.

// ownerUID is the user the app runs as; the control socket and the log
// belong to them.
var ownerUID int

func setTrayState() {}

// showError is how fatal reports: the engine keeps the message for the app
// to pick up and then idles until it is told to stop, since fatal's callers
// do not expect it to return.
func showError(msg string) {
	failure.CompareAndSwap(nil, &msg)
	select {}
}

func main() {
	printConfig := flag.Bool("print-config", false, "print the config file as JSON")
	saveConfig := flag.Bool("save-config", false, "write the JSON on stdin to the config file")
	flag.StringVar(&cfgPath, "config", "", "config file")
	flag.StringVar(&logPath, "log", "", "log file")
	flag.IntVar(&ownerUID, "uid", 0, "owner of the control socket")
	flag.BoolVar(&debug, "debug", false, "log every captured connection")
	flag.Parse()

	var err error
	switch {
	case cfgPath == "":
		err = errors.New("-config is required")
	case *printConfig:
		err = dumpConfig()
	case *saveConfig:
		err = storeConfig()
	case os.Geteuid() != 0:
		err = errors.New("the engine must run as root; the app installs it by itself")
	default:
		err = engine()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dumpConfig() error {
	cfg, err := loadConfig(cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		cfg, err = &Config{}, nil
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(cfg)
}

func storeConfig() error {
	var cfg Config
	if err := json.NewDecoder(os.Stdin).Decode(&cfg); err != nil {
		return err
	}
	// write a new file and swap it in, so the engine never reads half of one
	f, err := os.CreateTemp(filepath.Dir(cfgPath), ".basicsplit-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := toml.NewEncoder(f).Encode(&cfg); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), cfgPath)
}

func engine() error {
	// the pf rules leave this group's sockets alone
	if err := unix.Setegid(exemptGID); err != nil {
		return err
	}
	lock, err := os.OpenFile("/var/run/basicsplit.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return errors.New("the engine is already running")
	}
	if logPath != "" {
		openLog()
		os.Chown(logPath, ownerUID, -1)
	}
	return serveControl()
}
