// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "CuminStatusBar",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "CuminStatusBar",
            path: "Sources/CuminStatusBar"),
        .testTarget(
            name: "CuminStatusBarTests",
            dependencies: ["CuminStatusBar"],
            path: "Tests/CuminStatusBarTests"),
    ]
)
