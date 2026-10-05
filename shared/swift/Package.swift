// swift-tools-version:5.9
//
// SwiftPM manifest for the shared Swift library.
//
// The macOS apps do NOT use this package: `Tools/build_mac_app.sh` compiles every file under
// `Sources/Shared` straight into each app with `swiftc`. The package exists so the
// Foundation-only parts (framing, crypto, WebSocket framing, HTTP parsing, JSON helpers) can
// be unit-tested with `swift test` — on the macOS CI runner and on Linux. Every macOS-only
// source file is wrapped in `#if canImport(AppKit)` / `#if canImport(Network)` so it compiles
// to nothing on Linux.
//
// CryptoKit does not exist on Linux, so there the crypto files `import Crypto` from
// apple/swift-crypto instead. The dependency is declared only when the manifest is evaluated
// on Linux; on macOS package resolution needs no network access at all.
import PackageDescription

#if os(Linux)
let packageDependencies: [Package.Dependency] = [
    .package(url: "https://github.com/apple/swift-crypto.git", from: "3.0.0"),
]
let sharedDependencies: [Target.Dependency] = [
    .product(name: "Crypto", package: "swift-crypto"),
]
#else
let packageDependencies: [Package.Dependency] = []
let sharedDependencies: [Target.Dependency] = []
#endif

let package = Package(
    name: "Shared",
    platforms: [
        .macOS(.v14),
    ],
    products: [
        .library(name: "Shared", targets: ["Shared"]),
    ],
    dependencies: packageDependencies,
    targets: [
        .target(
            name: "Shared",
            dependencies: sharedDependencies,
            path: "Sources/Shared"
        ),
        .testTarget(
            name: "SharedTests",
            dependencies: ["Shared"],
            path: "Tests/SharedTests"
        ),
    ]
)
