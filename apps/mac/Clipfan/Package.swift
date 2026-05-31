// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "Clipfan",
    platforms: [.macOS(.v13)],
    dependencies: [
        .package(url: "https://github.com/sindresorhus/KeyboardShortcuts", from: "2.0.0"),
        .package(url: "https://github.com/sparkle-project/Sparkle", from: "2.6.0"),
    ],
    targets: [
        .executableTarget(
            name: "Clipfan",
            dependencies: [
                .product(name: "KeyboardShortcuts", package: "KeyboardShortcuts"),
                .product(name: "Sparkle", package: "Sparkle"),
            ],
            path: "Sources/Clipfan"
        ),
        .testTarget(
            name: "ClipfanTests",
            dependencies: ["Clipfan"],
            path: "Tests/ClipfanTests"
        ),
    ]
)
