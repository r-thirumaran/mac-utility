import Foundation

/// The local text clipboard (`core.Clipboard`).
@MainActor
protocol TextClipboard: AnyObject {
    /// The clipboard text, `nil` when it holds no text.
    func text() -> String?
    func setText(_ text: String)
}
