import Foundation

/// Maps a request path onto a file under a root directory, safely.
///
/// Shared by `HTTPServer` (Reach's PWA) and `AppSchemeHandler` (`app://ui/…`). A path with a
/// `..` component is refused outright, the resolved URL must still lie under `root`, and a
/// directory resolves to its `index.html`.
enum StaticFileResolver {

    /// The file to serve for `path`, or `nil` when it does not exist or escapes `root`.
    static func resolve(path: String, under root: URL) -> URL? {
        var components: [String] = []
        for part in path.split(separator: "/", omittingEmptySubsequences: true) {
            let component = String(part)
            if component == "." { continue }
            if component == ".." { return nil }
            components.append(component)
        }

        var url = root
        for component in components {
            url.appendPathComponent(component)
        }
        url = url.standardizedFileURL

        let rootPath = root.standardizedFileURL.path
        guard url.path == rootPath || url.path.hasPrefix(rootPath + "/") else { return nil }

        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) else {
            return nil
        }
        if isDirectory.boolValue {
            let index = url.appendingPathComponent("index.html")
            return FileManager.default.fileExists(atPath: index.path) ? index : nil
        }
        return url
    }
}
