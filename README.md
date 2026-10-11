# basicsplit

A simple WireGuard split tunnel for Windows and macOS. Only the programs you pick, and only their traffic to the sites you pick, go through the VPN; everything else leaves over your normal connection.

Example: VPN only inside `chrome.exe`, and only for `wowhead.com`.

## How it works (Windows)

- The WireGuard tunnel lives inside the program (`wireguard-go` + netstack). No virtual network adapter is created and the system's route table is left alone.
- WinDivert redirects the outgoing TCP connections of the exes in the rule list to a local listener inside the program.
- The listener reads the domain from the first packet of the connection (TLS SNI or HTTP Host). If the domain is on the list the connection goes through the tunnel, otherwise it goes out directly.

## Setup (Windows)

1. Install [Go](https://go.dev/dl/) and build:

   ```
   build.bat
   ```

2. Download [WinDivert 2.2.2](https://github.com/basil00/WinDivert/releases/tag/v2.2.2) and put `WinDivert.dll` and `WinDivert64.sys` from its `x64` folder next to `basicsplit.exe`.
3. Put the WireGuard profile (`.conf`) from your VPN provider in the same folder.
4. Copy `basicsplit.example.toml` to `basicsplit.toml`, put your profile's name on the `wireguard` line and edit the rules.
5. Run `basicsplit.exe`. It asks for administrator rights (WinDivert needs them) and then sits in the system tray.

If Windows Firewall asks for permission on the first run, allow it; captured connections reach the program as incoming ones.

## macOS

The tunnel and the rules work the same way; the difference is in how connections are captured.

### How it works

- `pf`, the macOS packet filter, redirects outgoing TCP connections to the local listener inside the program. `pf` cannot tell programs apart, so every program's connections are captured; those without a rule are passed on directly, untouched.
- The listener finds the program that opened the connection. If it has a rule, the listener looks at the domain and picks the tunnel or the direct route.
- If every rule has `domains`, only ports 80 and 443 are captured; if any rule has none, all ports are.

### Setup

With Homebrew:

```
brew install --cask cancng/tap/basicsplit
```

Or from source, with [Go](https://go.dev/dl/) and Xcode installed:

```
./build.sh
```

Then move `build/basicsplit.app` to `/Applications`.

After that:

1. Open the app.
2. On the first launch it asks for an administrator password once, to install its engine. The engine goes into `/Library/PrivilegedHelperTools` and is run by launchd; the password is not asked again unless an update changes the engine.
3. Open **Settings** from the menu bar icon:
   - In **General**, import your WireGuard profile (`.conf`).
   - In **Rules**, add an app with `+` and list the sites that should go through the tunnel.

Changes apply within a few seconds; there is no save button.

Capturing stops when you quit the app. Settings are kept in `~/Library/Application Support/basicsplit/basicsplit.toml`, in the same format as on Windows; the log is at `~/Library/Logs/basicsplit.log`.

### Layout

- `basicsplit-engine`: the Go program in this repository. It runs as root and manages the tunnel and the `pf` rules.
- `macos/`: the menu bar icon and the settings window (SwiftUI). It installs the engine and controls it over `/var/run/basicsplit.sock`.
- `release.sh`: builds the signed and notarized release package and the Homebrew cask file.

### Limitations specific to macOS

- QUIC (UDP 443) is refused for everyone, not just for the programs in the rules. `allow_quic = true` turns that off.
- Safari is not supported: its traffic comes from the system's shared WebKit process, not from Safari itself.
- Programs behind a network extension such as AdGuard are recognized, but the extension's own connections can be delayed by up to a second.
- The program does not start if another VPN or firewall has replaced the main `pf` ruleset.
- If the program crashes and you lose connectivity, remove the rules by hand: `sudo pfctl -a com.apple/basicsplit -F all`

## Rules

On macOS the rules are edited in the Settings window; the file format below is for Windows.

```toml
wireguard = "profile.conf"

[[rule]]
exe = "chrome.exe"
domains = ["wowhead.com", "zamimg.com"]

[[rule]]
exe = "other.exe"   # without domains, all of the exe's TCP traffic goes through the tunnel
```

- A domain also covers its subdomains (`wowhead.com` → `www.wowhead.com`).
- Rules are reloaded a few seconds after you save the file. Restart the program for changes to `wireguard` and `listen`.

## Tray menu (Windows)

- **Enabled:** turns capturing on and off. Open connections are not cut when you turn it off.
- **Start with Windows:** creates a Task Scheduler task so the program starts at logon without a UAC prompt.
- **Edit Rules / Show Log:** open `basicsplit.toml` and `basicsplit.log`.

## Limitations

These are about Windows unless noted; the first and the third also apply to macOS.

- Only TCP is tunneled. Programs that use UDP (most games) are not supported.
- QUIC (UDP 443) traffic of the exes in the rule list is dropped; the browser falls back to TCP.
- If the browser encrypts the domain (ECH), the site does not match and goes out directly. Turning off "Use secure DNS" in Chrome prevents this.
- Some anti-cheat systems dislike the WinDivert driver; it is safer to quit the program while gaming.

## Built with

- [wireguard-go](https://git.zx2c4.com/wireguard-go/)
- [WinDivert](https://github.com/basil00/WinDivert)
- [fyne.io/systray](https://github.com/fyne-io/systray)
