import AppKit
import SwiftUI

struct SettingsView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        TabView {
            RulesView(model: model)
                .tabItem { Label("Rules", systemImage: "list.bullet") }
            GeneralView(model: model)
                .tabItem { Label("General", systemImage: "gearshape") }
        }
        .padding(12)
        .frame(minWidth: 620, minHeight: 400)
    }
}

// MARK: - Rules

struct RulesView: View {
    @ObservedObject var model: AppModel
    @State private var selection: Rule.ID?

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                List(selection: $selection) {
                    ForEach(model.config.rules) { rule in
                        RuleRow(rule: rule).tag(rule.id)
                    }
                }
                Divider()
                HStack(spacing: 2) {
                    Menu {
                        Button("Choose App…", action: addApp)
                        Button("Add by Name", action: addNamed)
                    } label: {
                        Image(systemName: "plus")
                    }
                    .menuStyle(.borderlessButton)
                    .menuIndicator(.hidden)
                    .fixedSize()
                    Button(action: removeSelected) {
                        Image(systemName: "minus")
                    }
                    .buttonStyle(.borderless)
                    .disabled(selection == nil)
                    Spacer()
                }
                .padding(6)
            }
            .frame(width: 210)

            Divider()

            if let rule = selectedRule {
                RuleDetail(rule: rule)
            } else {
                Text(model.config.rules.isEmpty ? "Click + to add an app" : "Select a rule")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .onAppear { selection = selection ?? model.config.rules.first?.id }
    }

    /// Looks the rule up by ID on every access, so a deleted rule can never
    /// leave a binding pointing past the end of the list.
    private var selectedRule: Binding<Rule>? {
        guard let id = selection, let current = model.config.rules.first(where: { $0.id == id }) else { return nil }
        return Binding(
            get: { model.config.rules.first(where: { $0.id == id }) ?? current },
            set: { new in
                if let i = model.config.rules.firstIndex(where: { $0.id == id }) { model.config.rules[i] = new }
            })
    }

    private func add(_ rule: Rule) {
        model.config.rules.append(rule)
        selection = rule.id
    }

    private func addApp() {
        let panel = NSOpenPanel()
        panel.title = "Choose an App"
        panel.directoryURL = URL(fileURLWithPath: "/Applications")
        panel.allowedContentTypes = [.application]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        add(Rule(exe: url.deletingPathExtension().lastPathComponent, allTraffic: false, domains: [Domain(value: "")]))
    }

    private func addNamed() {
        add(Rule(exe: "", allTraffic: false, domains: [Domain(value: "")]))
    }

    private func removeSelected() {
        model.config.rules.removeAll { $0.id == selection }
        selection = model.config.rules.first?.id
    }
}

struct RuleRow: View {
    let rule: Rule

    var body: some View {
        HStack(spacing: 8) {
            Image(nsImage: AppIcons.icon(for: rule.exe))
                .resizable()
                .frame(width: 24, height: 24)
            VStack(alignment: .leading, spacing: 1) {
                Text(rule.exe.isEmpty ? "Untitled" : rule.exe)
                    .lineLimit(1)
                Text(summary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }

    private var summary: String {
        if rule.allTraffic { return "All traffic" }
        let domains = rule.cleanDomains
        switch domains.count {
        case 0: return "No domains"
        case 1: return domains[0]
        default: return "\(domains[0]) +\(domains.count - 1)"
        }
    }
}

struct RuleDetail: View {
    @Binding var rule: Rule
    @FocusState private var focused: Domain.ID?

    var body: some View {
        Form {
            Section {
                TextField("Program", text: $rule.exe, prompt: Text("Google Chrome"))
            } footer: {
                Text("The name of an app (Google Chrome) or the file name of a command line tool (curl).")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }

            Section {
                Picker("Send through the tunnel", selection: $rule.allTraffic) {
                    Text("Only the sites I list").tag(false)
                    Text("All of the program's traffic").tag(true)
                }
                .pickerStyle(.radioGroup)
            }

            if !rule.allTraffic {
                Section {
                    ForEach($rule.domains) { $domain in
                        HStack {
                            TextField("Domain", text: $domain.value, prompt: Text("example.com"))
                                .labelsHidden()
                                .focused($focused, equals: domain.id)
                                .onSubmit(addDomain)
                            Button {
                                rule.domains.removeAll { $0.id == domain.id }
                            } label: {
                                Image(systemName: "minus.circle")
                            }
                            .buttonStyle(.borderless)
                            .foregroundStyle(.secondary)
                        }
                    }
                    Button("Add Domain", action: addDomain)
                } header: {
                    Text("Sites")
                } footer: {
                    Text("Subdomains are included: example.com also covers www.example.com.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
        .formStyle(.grouped)
    }

    private func addDomain() {
        let domain = Domain(value: "")
        rule.domains.append(domain)
        focused = domain.id
    }
}

enum AppIcons {
    private static let folders = ["/Applications", "/System/Applications", NSHomeDirectory() + "/Applications"]
    private static let fallback = NSImage(systemSymbolName: "terminal", accessibilityDescription: nil) ?? NSImage()

    static func icon(for name: String) -> NSImage {
        for folder in folders {
            let path = "\(folder)/\(name).app"
            if !name.isEmpty, FileManager.default.fileExists(atPath: path) {
                return NSWorkspace.shared.icon(forFile: path)
            }
        }
        return fallback
    }
}

// MARK: - General

struct GeneralView: View {
    @ObservedObject var model: AppModel
    @State private var confirmUninstall = false

    var body: some View {
        Form {
            Section("WireGuard") {
                LabeledContent("Profile") {
                    HStack {
                        Text(model.hasProfile ? model.config.wireguard : "None")
                            .foregroundStyle(model.hasProfile ? .primary : .secondary)
                        Button("Import…", action: model.importProfile)
                    }
                }
            }

            Section {
                Toggle("Open at login", isOn: Binding(get: { model.launchAtLogin }, set: model.setLaunchAtLogin))
                Toggle("Allow QUIC", isOn: $model.config.allowQUIC)
            } footer: {
                Text("QUIC (UDP 443) hides the domain, so it is refused for every program and browsers fall back to TCP. If you allow it, sites that use QUIC bypass the tunnel.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }

            Section("Engine") {
                LabeledContent("Status", value: model.statusLine)
                HStack {
                    if model.installed {
                        Button("Restart", action: model.restart)
                        Button("Uninstall…") { confirmUninstall = true }
                    } else {
                        Button("Install…", action: model.install)
                    }
                    Spacer()
                    Button("Show Log") { NSWorkspace.shared.open(Paths.log) }
                }
            }

            if let problem = model.problem {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.red)
                }
            }

            Section {
            } footer: {
                Text("basicsplit \(model.version)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity)
            }
        }
        .formStyle(.grouped)
        .confirmationDialog("Uninstall the basicsplit engine?", isPresented: $confirmUninstall) {
            Button("Uninstall", role: .destructive, action: model.uninstall)
        } message: {
            Text("This removes the background helper and its launch entry. Your rules and profile stay where they are.")
        }
    }
}
