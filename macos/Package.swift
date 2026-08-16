// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "GogumaUI",
    // macOS 14 (Sonoma), which is the real floor.
    //
    // This said .v26, the current release, which locked the app to Macs
    // updated in the last few months for no reason anyone had checked:
    // building against .v14 produces zero errors, so nothing in the app uses
    // an API newer than that. Swift's availability checker is the authority
    // here, and it says 14. (13 fails: `@Observable` and the modern
    // `onChange` both landed in 14.)
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "GogumaUI",
            path: "Sources/GogumaUI",
            // The vendored package's licence travels with its source, as MIT
            // requires, but it is not code and not a resource.
            exclude: ["Vendor/FluidMenuBarExtra/LICENSE"],
            swiftSettings: [.swiftLanguageMode(.v6)]
        )
    ]
)
