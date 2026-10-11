// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "basicsplit",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "basicsplit", path: "Sources/basicsplit")
    ],
    swiftLanguageVersions: [.v5]
)
