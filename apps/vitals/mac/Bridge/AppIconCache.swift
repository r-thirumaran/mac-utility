import AppKit

/// Names and 32 px PNG icons of the running GUI apps, from `NSWorkspace.runningApplications`.
///
/// `NSRunningApplication` is the only source of localized app names ("Safari" rather than
/// the executable name) and of icons; both are AppKit, so this lives on the main actor and
/// hands the process sampler a plain value map. Icons are rendered once per bundle
/// identifier and cached, and only when the caller asks (`vitals.processes {icons: true}`):
/// the UI does not draw them yet, and sixty base64 PNGs every two seconds would be wasted
/// bridge traffic.
@MainActor
final class AppIconCache {

    private var icons: [String: String] = [:]

    // MARK: - Lookup

    func identities(includeIcons: Bool) -> [pid_t: ProcessSampler.AppIdentity] {
        var result: [pid_t: ProcessSampler.AppIdentity] = [:]
        for app in NSWorkspace.shared.runningApplications {
            let pid = app.processIdentifier
            guard pid > 0, let name = app.localizedName, !name.isEmpty else { continue }
            let icon = includeIcons ? encodedIcon(for: app) : nil
            result[pid] = ProcessSampler.AppIdentity(name: name, icon: icon)
        }
        return result
    }

    // MARK: - Icons

    private func encodedIcon(for app: NSRunningApplication) -> String? {
        let key = app.bundleIdentifier ?? app.bundleURL?.path ?? "pid-\(app.processIdentifier)"
        if let cached = icons[key] { return cached }
        guard let image = app.icon, let encoded = AppIconCache.png(image, side: 32) else { return nil }
        icons[key] = encoded
        return encoded
    }

    /// Draws `image` into a `side`×`side` bitmap and returns it as base64 PNG.
    private static func png(_ image: NSImage, side: Int) -> String? {
        guard let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil,
                                            pixelsWide: side,
                                            pixelsHigh: side,
                                            bitsPerSample: 8,
                                            samplesPerPixel: 4,
                                            hasAlpha: true,
                                            isPlanar: false,
                                            colorSpaceName: .deviceRGB,
                                            bytesPerRow: 0,
                                            bitsPerPixel: 0),
              let context = NSGraphicsContext(bitmapImageRep: bitmap) else { return nil }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = context
        image.draw(in: NSRect(x: 0, y: 0, width: side, height: side),
                   from: .zero,
                   operation: .sourceOver,
                   fraction: 1)
        NSGraphicsContext.restoreGraphicsState()
        return bitmap.representation(using: .png, properties: [:])?.base64EncodedString()
    }
}
