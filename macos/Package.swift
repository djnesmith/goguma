// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "WakeGuardUI",
    platforms: [.macOS(.v26)],
    targets: [
        .executableTarget(
            name: "WakeGuardUI",
            path: "Sources/WakeGuardUI",
            // The vendored package's licence travels with its source, as MIT
            // requires, but it is not code and not a resource.
            exclude: ["Vendor/FluidMenuBarExtra/LICENSE"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        )
    ]
)
