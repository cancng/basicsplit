package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

// At boot launchd starts the engine as root, so no password is asked, and at
// login it starts the tray icon. The engine is restarted if it crashes, which
// also clears pf rules it left behind, but not after a normal quit.
const (
	daemonPlist = "/Library/LaunchDaemons/basicsplit.engine.plist"
	agentPlist  = "Library/LaunchAgents/basicsplit.tray.plist" // under the home directory
)

const plistXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>RunAtLoad</key>
	<true/>
%s</dict>
</plist>
`

func plist(label, extra string, args ...string) []byte {
	var list bytes.Buffer
	for _, a := range args {
		list.WriteString("\t\t<string>")
		xml.EscapeText(&list, []byte(a))
		list.WriteString("</string>\n")
	}
	return []byte(fmt.Sprintf(plistXML, label, list.String(), extra))
}

func autostartEnabled() bool {
	_, err := os.Stat(daemonPlist)
	return err == nil
}

func setAutostart(on bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	agent := filepath.Join(home, agentPlist)
	if !on {
		os.Remove(agent)
		return adminRun("rm -f " + daemonPlist)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// the daemon's plist must belong to root, so stage it and install it
	tmp, err := os.CreateTemp("", "basicsplit-*.plist")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	tmp.Write(plist("basicsplit.engine",
		"\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n",
		exe, "-engine", "-config", cfgPath))
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := adminRun("install -o root -g wheel -m 644 " + shellQuote(tmp.Name()) + " " + daemonPlist); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		return err
	}
	return os.WriteFile(agent, plist("basicsplit.tray",
		"\t<key>LimitLoadToSessionType</key>\n\t<string>Aqua</string>\n",
		exe, "-config", cfgPath), 0o644)
}
