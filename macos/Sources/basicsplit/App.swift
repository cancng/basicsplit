import AppKit
import SwiftUI

@main
struct BasicSplitApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @ObservedObject private var model = AppModel.shared

    var body: some Scene {
        MenuBarExtra {
            MenuContent(model: model)
        } label: {
            Image(nsImage: Glyph.menuBar(active: model.isActive))
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        AppModel.shared.bootstrap()
    }

    func applicationWillTerminate(_ notification: Notification) {
        AppModel.shared.stopEngine()
    }
}

struct MenuContent: View {
    @ObservedObject var model: AppModel

    var body: some View {
        Text(model.statusLine)
        if let problem = model.problem {
            Text(problem)
        }
        Divider()
        if !model.installed {
            Button("Install Engine…") { model.install() }
        } else if model.status?.state == "failed" {
            Button("Try Again") { model.restart() }
        } else {
            Toggle("Enabled", isOn: Binding(get: { model.isActive }, set: model.setEnabled))
                .disabled(!model.isRunning)
        }
        Divider()
        Button("Settings…") { SettingsWindow.show() }
            .keyboardShortcut(",")
        Button("Show Log") { NSWorkspace.shared.open(Paths.log) }
        Divider()
        Button("Quit basicsplit") { model.quit() }
            .keyboardShortcut("q")
    }
}

/// The settings window is managed by hand: an app without a Dock icon has to
/// activate itself, or the window opens behind everything else.
enum SettingsWindow {
    private static var window: NSWindow?

    static func show() {
        if window == nil {
            let w = NSWindow(contentViewController: NSHostingController(rootView: SettingsView(model: .shared)))
            w.title = "basicsplit"
            w.styleMask = [.titled, .closable, .miniaturizable, .resizable]
            w.isReleasedWhenClosed = false
            w.setContentSize(NSSize(width: 680, height: 460))
            w.center()
            window = w
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}

enum Glyph {
    /// One line that splits into two, as a template image so the menu bar
    /// colors it for light and dark mode. Dimmed while nothing is captured.
    static func menuBar(active: Bool) -> NSImage {
        let image = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { _ in
            NSColor.black.withAlphaComponent(active ? 1 : 0.4).set()
            let lines = NSBezierPath()
            lines.lineWidth = 1.6
            lines.lineCapStyle = .round
            lines.lineJoinStyle = .round
            lines.move(to: NSPoint(x: 3, y: 9))
            lines.line(to: NSPoint(x: 8.5, y: 9))
            lines.line(to: NSPoint(x: 14.5, y: 13.5))
            lines.move(to: NSPoint(x: 8.5, y: 9))
            lines.line(to: NSPoint(x: 14.5, y: 4.5))
            lines.stroke()
            for dot in [NSPoint(x: 3, y: 9), NSPoint(x: 14.5, y: 13.5), NSPoint(x: 14.5, y: 4.5)] {
                NSBezierPath(ovalIn: NSRect(x: dot.x - 2, y: dot.y - 2, width: 4, height: 4)).fill()
            }
            return true
        }
        image.isTemplate = true
        return image
    }
}
