package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

func messageBox(msg string, flags uint32) {
	title, _ := windows.UTF16PtrFromString("basicsplit")
	text, _ := windows.UTF16PtrFromString(msg)
	windows.MessageBox(0, text, title, flags)
}

func showError(msg string) { messageBox(msg, windows.MB_ICONERROR) }
func showInfo(msg string)  { messageBox(msg, windows.MB_ICONINFORMATION) }

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
		fatal("Could not get administrator rights: %v", err)
	}
	os.Exit(0)
}

// singleInstance reports false if another copy is already running.
func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Global\basicsplit`)
	_, err := windows.CreateMutex(nil, false, name)
	return err != windows.ERROR_ALREADY_EXISTS
}

// runUI shows the tray icon and blocks until the user quits.
func runUI(logPath string) {
	waitForTaskbar()
	systray.Run(func() { onReady(logPath) }, nil)
	if !quitting.Load() {
		log.Print("could not create the tray icon, continuing without one")
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
				log.Printf("tray became ready after %d s", i)
			}
			return
		}
		time.Sleep(time.Second)
	}
	log.Print("tray was not ready within 2 minutes")
}

func main() {
	exe, err := os.Executable()
	if err != nil {
		fatal("%v", err)
	}
	exeDir := filepath.Dir(exe)

	flag.StringVar(&cfgPath, "config", filepath.Join(exeDir, "basicsplit.toml"), "config file")
	flag.BoolVar(&debug, "debug", false, "log every captured connection")
	flag.Parse()
	if cfgPath, err = filepath.Abs(cfgPath); err != nil {
		fatal("%v", err)
	}

	logPath = filepath.Join(exeDir, "basicsplit.log")

	elevate(exe)

	if !singleInstance() {
		showInfo("basicsplit is already running (look in the system tray).")
		return
	}

	openLog()

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fatal("Could not read the config: %v", err)
	}
	current.Store(cfg)
	enabled.Store(true)

	// the tunnel must not depend on the tray icon
	go run()

	runUI(logPath)
	cleanup()
}
