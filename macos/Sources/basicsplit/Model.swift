import AppKit
import ServiceManagement
import SwiftUI

final class AppModel: ObservableObject {
    static let shared = AppModel()

    /// nil while the engine cannot be reached.
    @Published var status: EngineStatus?
    @Published var installed = false
    @Published var launchAtLogin = false
    /// A problem on the app's side (install, config file, login item).
    @Published var problem: String?
    @Published var config = Config() {
        didSet { if loaded, config != oldValue { scheduleSave() } }
    }

    private var loaded = false
    private var saved = Config()
    private var pendingSave: DispatchWorkItem?
    private var timer: Timer?
    private let queue = DispatchQueue(label: "basicsplit.engine")

    // MARK: State shown in the UI

    var isRunning: Bool { status?.state == "running" }
    var isActive: Bool { isRunning && status?.enabled == true }
    var hasProfile: Bool { !config.wireguard.isEmpty }
    var version: String { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "" }

    var statusLine: String {
        guard installed else { return "Engine not installed" }
        guard hasProfile else { return "No WireGuard profile" }
        guard let status else { return "Engine not responding" }
        switch status.state {
        case "running": return status.enabled ? "Tunnel on" : "Paused"
        case "starting": return "Connecting…"
        case "failed": return "Error: " + (status.error ?? "unknown")
        default: return "Idle"
        }
    }

    // MARK: Lifecycle

    func bootstrap() {
        do {
            try FileManager.default.createDirectory(at: Paths.supportDir, withIntermediateDirectories: true)
            config = try ConfigStore.load()
        } catch {
            problem = "Could not read the settings: \(error.localizedDescription)"
        }
        saved = config
        loaded = true
        launchAtLogin = SMAppService.mainApp.status == .enabled

        Installer.removeLegacyAgent()
        installed = Installer.isCurrent
        if !installed { promptInstall() }
        if !hasProfile { SettingsWindow.show() }

        timer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in self?.poll() }
        poll()
    }

    /// Asks the engine how it is doing and starts it if it sits idle.
    private func poll() {
        guard installed else { return }
        // the engine reads the file, so wait until the profile is in it
        let canStart = !saved.wireguard.isEmpty
        queue.async {
            var status = Engine.send("status")
            if status?.state == "idle", canStart {
                status = Engine.send("start")
            }
            DispatchQueue.main.async {
                if self.status != status { self.status = status }
            }
        }
    }

    private func send(_ command: String) {
        queue.async {
            let status = Engine.send(command)
            DispatchQueue.main.async { self.status = status }
        }
    }

    func setEnabled(_ on: Bool) { send(on ? "on" : "off") }

    /// Stopping makes the engine exit; launchd brings up a fresh one, which
    /// the next poll starts again.
    func restart() { send("stop") }

    func quit() {
        pendingSave?.perform()
        NSApp.terminate(nil)
    }

    /// Called as the app goes away, so capturing does not outlive it.
    func stopEngine() {
        timer?.invalidate()
        _ = queue.sync { Engine.send("stop") }
    }

    // MARK: Engine installation

    private func promptInstall() {
        let alert = NSAlert()
        alert.messageText = Installer.isInstalled ? "basicsplit needs to update its engine" : "basicsplit needs to install its engine"
        alert.informativeText = "Routing the traffic of the apps you choose takes a helper that runs in the background. You will be asked for an administrator password once to install it."
        alert.addButton(withTitle: "Install")
        alert.addButton(withTitle: "Not Now")
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertFirstButtonReturn { install() }
    }

    func install() {
        do {
            try Installer.install()
            problem = nil
        } catch let error as InstallError where error.cancelled {
        } catch {
            problem = "Could not install the engine: \(error.localizedDescription)"
        }
        installed = Installer.isCurrent
        poll()
    }

    func uninstall() {
        do {
            try Installer.uninstall()
            problem = nil
        } catch let error as InstallError where error.cancelled {
        } catch {
            problem = "Could not uninstall the engine: \(error.localizedDescription)"
        }
        installed = Installer.isCurrent
        status = nil
    }

    // MARK: Settings

    private func scheduleSave() {
        pendingSave?.cancel()
        let work = DispatchWorkItem { [weak self] in self?.save() }
        pendingSave = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5, execute: work)
    }

    private func save() {
        pendingSave = nil
        do {
            try ConfigStore.save(config)
            problem = nil
        } catch {
            problem = "Could not save the settings: \(error.localizedDescription)"
            return
        }
        // The engine picks up rule changes by itself, but reads the profile
        // only when it starts.
        let profileChanged = config.wireguard != saved.wireguard
        saved = config
        if profileChanged { restart() }
    }

    /// Copies a WireGuard profile next to the config file and switches to it.
    func importProfile() {
        let panel = NSOpenPanel()
        panel.title = "Choose a WireGuard Profile"
        panel.allowedContentTypes = [.init(filenameExtension: "conf")].compactMap { $0 }
        panel.allowsOtherFileTypes = true
        NSApp.activate(ignoringOtherApps: true)
        guard panel.runModal() == .OK, let source = panel.url else { return }

        let target = Paths.supportDir.appendingPathComponent(source.lastPathComponent)
        do {
            let fm = FileManager.default
            try fm.createDirectory(at: Paths.supportDir, withIntermediateDirectories: true)
            if source.standardizedFileURL != target.standardizedFileURL {
                if fm.fileExists(atPath: target.path) { try fm.removeItem(at: target) }
                try fm.copyItem(at: source, to: target)
            }
            // it holds a private key
            try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: target.path)
        } catch {
            problem = "Could not copy the profile: \(error.localizedDescription)"
            return
        }
        if config.wireguard == target.lastPathComponent {
            restart() // same name, new contents
        } else {
            config.wireguard = target.lastPathComponent
        }
    }

    func setLaunchAtLogin(_ on: Bool) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            problem = nil
        } catch {
            problem = "Could not change the login item: \(error.localizedDescription)"
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }
}
