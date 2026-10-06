import AppKit

/// `open.url {url}` and `app.openURL`: http(s) only — a `file:` URL could run a `.command`
/// or `.app`, so nothing else is accepted.
enum URLOpener {

    /// The validated URL, or `invalid_params`.
    static func validated(_ raw: String) throws -> URL {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let lowered = trimmed.lowercased()
        guard lowered.hasPrefix("http://") || lowered.hasPrefix("https://"), let url = URL(string: trimmed) else {
            throw ReachError.invalidParams("only http(s) URLs can be opened")
        }
        return url
    }

    @MainActor
    static func open(_ raw: String) throws {
        let url = try validated(raw)
        guard NSWorkspace.shared.open(url) else {
            throw ReachError.internalError("the URL could not be opened")
        }
    }
}
