import Foundation

struct Domain: Identifiable, Equatable {
    let id = UUID()
    var value: String
}

struct Rule: Identifiable, Equatable {
    let id = UUID()
    /// App name ("Google Chrome") or executable name ("curl").
    var exe: String
    /// Tunnel everything the app sends instead of only the listed domains.
    var allTraffic: Bool
    var domains: [Domain]

    var cleanDomains: [String] {
        domains.map { $0.value.trimmingCharacters(in: .whitespaces).lowercased() }.filter { !$0.isEmpty }
    }

    /// A rule that is still being typed in must not reach the engine: with no
    /// domains it would mean "tunnel everything".
    var isComplete: Bool {
        !exe.trimmingCharacters(in: .whitespaces).isEmpty && (allTraffic || !cleanDomains.isEmpty)
    }
}

struct Config: Equatable {
    var wireguard = ""
    var listen = ""
    var allowQUIC = false
    var rules: [Rule] = []
}

/// The config file is TOML, shared with the Windows version. The engine
/// binary knows the format, so reading and writing goes through it as JSON.
enum ConfigStore {
    private struct FileRule: Codable {
        var exe: String
        var domains: [String]?
    }

    private struct File: Codable {
        var wireguard: String
        var listen: String
        var allow_quic: Bool
        var rules: [FileRule]?
    }

    static func load() throws -> Config {
        let out = try runEngine(["-print-config"], input: nil)
        let file = try JSONDecoder().decode(File.self, from: out)
        return Config(
            wireguard: file.wireguard,
            listen: file.listen,
            allowQUIC: file.allow_quic,
            rules: (file.rules ?? []).map { rule in
                let domains = rule.domains ?? []
                return Rule(exe: rule.exe, allTraffic: domains.isEmpty, domains: domains.map { Domain(value: $0) })
            })
    }

    static func save(_ config: Config) throws {
        let file = File(
            wireguard: config.wireguard,
            listen: config.listen,
            allow_quic: config.allowQUIC,
            rules: config.rules.filter(\.isComplete).map { rule in
                FileRule(
                    exe: rule.exe.trimmingCharacters(in: .whitespaces),
                    domains: rule.allTraffic ? [] : rule.cleanDomains)
            })
        try FileManager.default.createDirectory(at: Paths.supportDir, withIntermediateDirectories: true)
        _ = try runEngine(["-save-config"], input: JSONEncoder().encode(file))
    }

    private static func runEngine(_ arguments: [String], input: Data?) throws -> Data {
        let process = Process()
        process.executableURL = Paths.bundledEngine
        process.arguments = ["-config", Paths.config.path] + arguments
        let stdin = Pipe(), stdout = Pipe(), stderr = Pipe()
        process.standardInput = stdin
        process.standardOutput = stdout
        process.standardError = stderr
        try process.run()
        if let input { stdin.fileHandleForWriting.write(input) }
        stdin.fileHandleForWriting.closeFile()
        let out = stdout.fileHandleForReading.readDataToEndOfFile()
        let err = stderr.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw InstallError(message: String(decoding: err, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return out
    }
}
