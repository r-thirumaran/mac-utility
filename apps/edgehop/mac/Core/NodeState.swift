import Foundation

/// The core part of the `edgehop.state` payload (`core.State`); the bridge adds `pairing`,
/// `launchAtLogin`, `version` and `fakeInput` on top, as `win/bridge.go` does.
struct NodeState: Codable {

    var selfMachine: Machine
    var peers: [Machine]
    var layout: Layout
    var active: String
    /// `idle` | `sending` | `receiving`.
    var role: String
    var permissions: [String: Bool]
    var settings: EdgehopSettings
    var port: Int

    private enum CodingKeys: String, CodingKey {
        case selfMachine = "self"
        case peers, layout, active, role, permissions, settings, port
    }
}
