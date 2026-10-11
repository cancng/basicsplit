import AppKit
import Foundation

enum Paths {
    static let supportDir = FileManager.default
        .urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("basicsplit")
    static let config = supportDir.appendingPathComponent("basicsplit.toml")
    static let log = FileManager.default
        .urls(for: .libraryDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Logs/basicsplit.log")

    static let socket = "/var/run/basicsplit.sock"
    static let installedEngine = "/Library/PrivilegedHelperTools/basicsplit-engine"
    static let daemonPlist = "/Library/LaunchDaemons/basicsplit.engine.plist"
    static let daemonLabel = "basicsplit.engine"

    /// The engine binary that ships inside the app bundle.
    static var bundledEngine: URL {
        Bundle.main.url(forAuxiliaryExecutable: "basicsplit-engine")
            ?? Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/basicsplit-engine")
    }
}

struct EngineStatus: Decodable, Equatable {
    /// idle, starting, running or failed
    var state: String
    var enabled: Bool
    var error: String?
}

/// The engine is a root daemon that owns the tunnel and the pf rules. It takes
/// one command per connection on a unix socket and answers with its status.
enum Engine {
    /// Returns nil if the engine cannot be reached.
    static func send(_ command: String) -> EngineStatus? {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { return nil }
        defer { close(fd) }

        var timeout = timeval(tv_sec: 2, tv_usec: 0)
        let size = socklen_t(MemoryLayout<timeval>.size)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, size)
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, size)

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let capacity = MemoryLayout.size(ofValue: addr.sun_path)
        withUnsafeMutablePointer(to: &addr.sun_path) {
            $0.withMemoryRebound(to: CChar.self, capacity: capacity) {
                _ = strlcpy($0, Paths.socket, capacity)
            }
        }
        let connected = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connected == 0 else { return nil }

        let line = Array((command + "\n").utf8)
        guard write(fd, line, line.count) == line.count else { return nil }

        var reply = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while !reply.contains(UInt8(ascii: "\n")) {
            let n = read(fd, &buffer, buffer.count)
            if n <= 0 { break }
            reply.append(buffer, count: n)
        }
        return try? JSONDecoder().decode(EngineStatus.self, from: reply)
    }
}

struct InstallError: LocalizedError {
    var message: String
    var cancelled = false
    var errorDescription: String? { message }
}

/// Installs the engine where only root can change it and registers it with
/// launchd, which keeps it running. This is the one step that needs an
/// administrator password.
enum Installer {
    private static func daemonPlistData() -> Data {
        let plist: [String: Any] = [
            "Label": Paths.daemonLabel,
            "ProgramArguments": [
                Paths.installedEngine,
                "-config", Paths.config.path,
                "-log", Paths.log.path,
                "-uid", String(getuid()),
            ],
            "RunAtLoad": true,
            "KeepAlive": true,
            "ThrottleInterval": 1,
        ]
        return try! PropertyListSerialization.data(fromPropertyList: plist, format: .xml, options: 0)
    }

    /// Whether the installed engine is the one this version of the app ships.
    static var isCurrent: Bool {
        let fm = FileManager.default
        return fm.contentsEqual(atPath: Paths.bundledEngine.path, andPath: Paths.installedEngine)
            && fm.contents(atPath: Paths.daemonPlist) == daemonPlistData()
    }

    static var isInstalled: Bool {
        FileManager.default.fileExists(atPath: Paths.daemonPlist)
    }

    private static let bootout = """
        launchctl bootout system/\(Paths.daemonLabel) 2>/dev/null; \
        for i in 1 2 3 4 5 6 7 8 9 10; do \
        launchctl print system/\(Paths.daemonLabel) >/dev/null 2>&1 || break; sleep 0.3; done
        """

    static func install() throws {
        let staged = FileManager.default.temporaryDirectory
            .appendingPathComponent("basicsplit-\(UUID().uuidString).plist")
        try daemonPlistData().write(to: staged)
        defer { try? FileManager.default.removeItem(at: staged) }

        try runAsAdmin("""
            \(bootout); \
            mkdir -p /Library/PrivilegedHelperTools && \
            install -o root -g wheel -m 755 \(quote(Paths.bundledEngine.path)) \(Paths.installedEngine) && \
            install -o root -g wheel -m 644 \(quote(staged.path)) \(Paths.daemonPlist) && \
            launchctl bootstrap system \(Paths.daemonPlist)
            """)
    }

    static func uninstall() throws {
        try runAsAdmin("\(bootout); rm -f \(Paths.installedEngine) \(Paths.daemonPlist) \(Paths.socket)")
    }

    /// Earlier versions ran the tray icon from a LaunchAgent; this app
    /// replaces it.
    static func removeLegacyAgent() {
        let agent = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/basicsplit.tray.plist")
        guard FileManager.default.fileExists(atPath: agent.path) else { return }
        let launchctl = Process()
        launchctl.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        launchctl.arguments = ["bootout", "gui/\(getuid())/basicsplit.tray"]
        try? launchctl.run()
        launchctl.waitUntilExit()
        try? FileManager.default.removeItem(at: agent)
    }

    private static func quote(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    private static func runAsAdmin(_ script: String) throws {
        let escaped = script
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        let source = "do shell script \"\(escaped)\" with administrator privileges"
        var error: NSDictionary?
        NSAppleScript(source: source)?.executeAndReturnError(&error)
        guard let error else { return }
        let cancelled = (error[NSAppleScript.errorNumber] as? Int) == -128
        throw InstallError(
            message: error[NSAppleScript.errorMessage] as? String ?? "Unknown error",
            cancelled: cancelled)
    }
}
