package main

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const taskName = "basicsplit"

// A scheduled task is the only way to start elevated at logon without a UAC
// prompt. It is created from XML because the schtasks command-line defaults
// would kill the task after 72 hours and refuse to start it on battery.
const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>%[1]s</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%[1]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%[2]s</Command>
      <Arguments>%[3]s</Arguments>
      <WorkingDirectory>%[4]s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`

func schtasks(args ...string) error {
	cmd := exec.Command("schtasks.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func autostartEnabled() bool {
	return schtasks("/Query", "/TN", taskName) == nil
}

func setAutostart(on bool) error {
	if !on {
		return schtasks("/Delete", "/TN", taskName, "/F")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	doc := fmt.Sprintf(taskXML, xmlEscape(u.Username), xmlEscape(exe),
		xmlEscape(windows.ComposeCommandLine([]string{"-config", cfgPath})), xmlEscape(filepath.Dir(exe)))

	// schtasks wants UTF-16 with a BOM
	f, err := os.CreateTemp("", "basicsplit-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	f.Write([]byte{0xff, 0xfe})
	binary.Write(f, binary.LittleEndian, utf16.Encode([]rune(doc)))
	if err := f.Close(); err != nil {
		return err
	}
	return schtasks("/Create", "/TN", taskName, "/XML", f.Name(), "/F")
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
