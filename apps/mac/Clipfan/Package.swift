// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "Clipfan",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "Clipfan",
            path: "Sources/Clipfan"
        ),
        .testTarget(
            name: "ClipfanTests",
            dependencies: ["Clipfan"],
            path: "Tests/ClipfanTests"
        ),
    ]
)
