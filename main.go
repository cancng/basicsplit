package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"fyne.io/systray"
	"github.com/BurntSushi/toml"
	"golang.org/x/sys/windows"
)

type Rule struct {
	Exe     string   `toml:"exe"`
	Domains []string `toml:"domains"`
}

type Config struct {
	WireGuard string `toml:"wireguard"`
	Listen    string `toml:"listen"`
	Rules     []Rule `toml:"rule"`
}

var (
	cfgPath string
	// current is swapped whenever the config file changes on disk
	current atomic.Pointer[Config]
	// enabled is the tray's on/off switch; when off, no new connections are
	// captured
	enabled atomic.Bool

	tunnelUp  atomic.Bool
	trayReady atomic.Bool
	quitting  atomic.Bool
)

// match reports whether host is one of the rule's domains or a subdomain of
// one.
func (r *Rule) match(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, d := range r.Domains {
		d = strings.ToLower(d)
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// matchDomain is used by the SOCKS5 proxy, which doesn't know the exe.
func (c *Config) matchDomain(host string) bool {
	for i := range c.Rules {
		if c.Rules[i].match(host) {
			return true
		}
	}
	return false
}

// ruleForExe takes a lowercase exe file name.
func (c *Config) ruleForExe(name string) *Rule {
	if name == "" {
		return nil
	}
	for i := range c.Rules {
		if strings.ToLower(c.Rules[i].Exe) == name {
			return &c.Rules[i]
		}
	}
	return nil
}

func (c *Config) logRules() {
	for _, rule := range c.Rules {
		if len(rule.Domains) == 0 {
			log.Printf("kural: %s -> tum TCP trafigi tunelden", rule.Exe)
		} else {
			log.Printf("kural: %s -> %s", rule.Exe, strings.Join(rule.Domains, ", "))
		}
	}
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func messageBox(msg string, flags uint32) {
	title, _ := windows.UTF16PtrFromString("basicsplit")
	text, _ := windows.UTF16PtrFromString(msg)
	windows.MessageBox(0, text, title, flags)
}

// fatal replaces log.Fatal: there is no console, so show the error.
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	messageBox(msg, windows.MB_ICONERROR)
	os.Exit(1)
}

// elevate relaunches the program as administrator if needed; WinDivert
// requires it.
func elevate(exe string) {
	if windows.GetCurrentProcessToken().IsElevated() {
		return
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine(os.Args[1:]))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err := windows.ShellExecute(0, verb, file, args, dir, windows.SW_SHOWNORMAL); err != nil {
		fatal("Yonetici yetkisi alinamadi: %v", err)
	}
	os.Exit(0)
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		fatal("%v", err)
	}
	exeDir := filepath.Dir(exe)

	flag.StringVar(&cfgPath, "config", filepath.Join(exeDir, "basicsplit.toml"), "kural dosyasi")
	flag.Parse()
	if cfgPath, err = filepath.Abs(cfgPath); err != nil {
		fatal("%v", err)
	}

	elevate(exe)

	name, _ := windows.UTF16PtrFromString(`Global\basicsplit`)
	if _, err := windows.CreateMutex(nil, false, name); err == windows.ERROR_ALREADY_EXISTS {
		messageBox("basicsplit zaten calisiyor (sistem tepsisine bak).", windows.MB_ICONINFORMATION)
		return
	}

	logPath := filepath.Join(exeDir, "basicsplit.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 1<<20 {
		os.Remove(logPath)
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(f)
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fatal("Config okunamadi: %v", err)
	}
	current.Store(cfg)
	enabled.Store(true)

	// the tunnel must not depend on the tray icon
	go run()

	waitForTaskbar()
	systray.Run(func() { onReady(logPath) }, nil)
	if !quitting.Load() {
		log.Print("tray simgesi olusturulamadi, simgesiz devam ediliyor")
		select {}
	}
}

// NOTIFYICONDATAW
type notifyIconData struct {
	Size             uint32
	Wnd              uintptr
	ID, Flags        uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State, StateMask uint32
	Info             [256]uint16
	Version          uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUID             windows.GUID
	BalloonIcon      uintptr
}

// waitForTaskbar blocks until Explorer accepts tray icons. Right after logon
// Shell_NotifyIcon fails while Explorer is still starting, and the systray
// package gives up for good on the first failure. So probe with a hidden
// throwaway icon until adding one works.
func waitForTaskbar() {
	const (
		nimAdd, nimDelete = 0, 2
		nifState          = 8
		nisHidden         = 1
	)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	user32 := windows.NewLazySystemDLL("user32.dll")
	shellNotifyIcon := windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")

	class, _ := windows.UTF16PtrFromString("STATIC")
	hwndMessage := ^uintptr(2) // HWND_MESSAGE (-3)
	hwnd, _, _ := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)),
		0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if hwnd == 0 {
		return
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)

	nid := notifyIconData{Wnd: hwnd, ID: 1, Flags: nifState, State: nisHidden, StateMask: nisHidden}
	nid.Size = uint32(unsafe.Sizeof(nid))
	for i := 0; i < 120; i++ {
		ok, _, _ := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
		// a failed add can still leave the icon behind
		shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		if ok != 0 {
			if i > 0 {
				log.Printf("tray %d sn sonra hazir oldu", i)
			}
			return
		}
		time.Sleep(time.Second)
	}
	log.Print("tray 2 dakikada hazir olmadi")
}

// run brings everything up and then stays in the packet loop.
func run() {
	cfg := current.Load()
	wgPath := cfg.WireGuard
	if !filepath.IsAbs(wgPath) {
		wgPath = filepath.Join(filepath.Dir(cfgPath), wgPath)
	}

	// At logon the network may not be up yet, so keep retrying the endpoint
	// lookup. Anything else is a real error.
	tnet, err := startTunnel(wgPath)
	for errors.Is(err, errResolve) {
		log.Printf("%v; 5 sn sonra tekrar denenecek", err)
		time.Sleep(5 * time.Second)
		tnet, err = startTunnel(wgPath)
	}
	if err != nil {
		fatal("Tunel baslatilamadi: %v", err)
	}
	log.Print("tunel hazir")
	tunnelUp.Store(true)
	setTrayState()

	// optional: plain SOCKS5 proxy
	if cfg.Listen != "" {
		p := &proxy{tnet: tnet}
		go func() { fatal("SOCKS5: %v", p.listen(cfg.Listen)) }()
		log.Printf("SOCKS5 %s uzerinde dinleniyor", cfg.Listen)
	}

	r, err := startRedirector(tnet)
	if err != nil {
		fatal("%v", err)
	}
	cfg.logRules()
	go watchConfig(r)
	fatal("%v", r.run())
}

// watchConfig reloads the rules when the config file is saved. Changes to
// "wireguard" and "listen" still need a restart.
func watchConfig(r *redirector) {
	var last time.Time
	if st, err := os.Stat(cfgPath); err == nil {
		last = st.ModTime()
	}
	for range time.Tick(2 * time.Second) {
		st, err := os.Stat(cfgPath)
		if err != nil || st.ModTime().Equal(last) {
			continue
		}
		last = st.ModTime()
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			log.Printf("config hatali, eski kurallar gecerli: %v", err)
			continue
		}
		current.Store(cfg)
		r.forgetVerdicts()
		log.Print("config yeniden yuklendi")
		cfg.logRules()
	}
}
