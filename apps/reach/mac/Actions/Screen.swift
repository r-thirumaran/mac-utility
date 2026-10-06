import AppKit
import CoreGraphics

/// Displays and screen frames for `screen.displays`, `screen.frame` and the stream
/// (`docs/reach-api.md` §2 and §4).
///
/// Capture is `CGDisplayCreateImage` (deprecated in 14 but still shipped for a 14.0
/// deployment target), downscaled with a `CGContext` draw to at most `maxWidth` pixels and
/// encoded as JPEG by `NSBitmapImageRep`. Without the Screen Recording grant the call
/// returns only the wallpaper, so the permission is checked first and reported as
/// `needs_permission {permission: "screenRecording"}`. Everything here is safe to call from
/// the stream's own queue; only the display *names* need AppKit (`displayRows`).
enum ScreenCapture {

    /// One physical display: index into `screen.displays`, global bounds in points
    /// (`CGDisplayBounds`, top-left origin), pixel size and whether it is the main display.
    struct Display {
        let id: CGDirectDisplayID
        let index: Int
        let bounds: CGRect
        let pixelWidth: Int
        let pixelHeight: Int
        let primary: Bool

        var scale: Double {
            bounds.width > 0 ? Double(pixelWidth) / Double(bounds.width) : 1
        }
    }

    static let maxFrameWidth = 1280
    static let defaultQuality = 0.6
    static let minQuality = 0.1
    static let maxQuality = 0.9
    static let minFPS = 1
    static let maxFPS = 10
    static let defaultFPS = 5
    static let frameKindJPEG: UInt8 = 0x01

    // MARK: - Displays

    /// Active displays, main first, then left to right and top to bottom.
    static func displays() -> [Display] {
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
        var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
        var filled: UInt32 = 0
        guard CGGetActiveDisplayList(count, &ids, &filled) == .success else { return [] }
        var rows: [Display] = []
        for id in ids.prefix(Int(filled)) {
            let bounds = CGDisplayBounds(id)
            rows.append(Display(id: id, index: 0, bounds: bounds,
                                pixelWidth: CGDisplayPixelsWide(id), pixelHeight: CGDisplayPixelsHigh(id),
                                primary: CGDisplayIsMain(id) != 0))
        }
        rows.sort { a, b in
            if a.primary != b.primary { return a.primary }
            if a.bounds.minX != b.bounds.minX { return a.bounds.minX < b.bounds.minX }
            return a.bounds.minY < b.bounds.minY
        }
        return rows.enumerated().map { offset, row in
            Display(id: row.id, index: offset, bounds: row.bounds, pixelWidth: row.pixelWidth,
                    pixelHeight: row.pixelHeight, primary: row.primary)
        }
    }

    /// The display at `index`, or `invalid_params`.
    static func display(at index: Int) throws -> Display {
        let all = displays()
        guard index >= 0, index < all.count else {
            throw ReachError.invalidParams("no display \(index)")
        }
        return all[index]
    }

    /// The union of every display's bounds, for clamping the pointer.
    static func desktopBounds() -> CGRect {
        displays().reduce(CGRect.null) { $0.union($1.bounds) }
    }

    /// `screen.displays` rows. Names come from `NSScreen`, hence main actor.
    @MainActor
    static func displayRows() -> [[String: Any]] {
        let screens = NSScreen.screens
        return displays().map { display -> [String: Any] in
            let screen = screens.first { screen in
                (screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber)?.uint32Value == display.id
            }
            let name = screen?.localizedName ?? "Display \(display.index + 1)"
            return [
                "index": display.index,
                "w": display.pixelWidth,
                "h": display.pixelHeight,
                "scale": display.scale,
                "primary": display.primary,
                "name": name,
            ]
        }
    }

    // MARK: - Parameters

    /// Clamps the stream parameters to the ranges of the spec (missing/zero → defaults).
    static func normalize(maxWidth: Int, quality: Double, fps: Int) -> (maxWidth: Int, quality: Double, fps: Int) {
        var width = maxWidth
        if width <= 0 || width > maxFrameWidth { width = maxFrameWidth }
        var q = quality == 0 ? defaultQuality : quality
        q = min(max(q, minQuality), maxQuality)
        var rate = fps
        if rate < minFPS { rate = defaultFPS }
        if rate > maxFPS { rate = maxFPS }
        return (width, q, rate)
    }

    /// Throws `needs_permission` when Screen Recording is not granted.
    static func ensurePermission() throws {
        guard Permissions.screenRecordingGranted else {
            throw ReachError.needsPermission("screenRecording",
                "Screen Recording is not allowed for Reach: open System Settings ▸ Privacy & Security ▸ Screen Recording, switch Reach on and relaunch it.")
        }
    }

    // MARK: - Capture

    /// Grabs display `index`, scaled to at most `maxWidth` pixels wide, as JPEG.
    static func capture(index: Int, maxWidth: Int, quality: Double) throws -> (width: Int, height: Int, jpeg: Data) {
        let target = try display(at: index)
        try ensurePermission()
        guard let image = CGDisplayCreateImage(target.id) else {
            throw ReachError.internalError("display \(index) could not be captured")
        }
        let scaled = downscale(image, maxWidth: maxWidth)
        guard scaled.width > 0, scaled.height > 0, scaled.width <= 0xFFFF, scaled.height <= 0xFFFF else {
            throw ReachError.internalError("frame \(scaled.width)x\(scaled.height) out of range")
        }
        guard let jpeg = encodeJPEG(scaled, quality: quality) else {
            throw ReachError.internalError("JPEG encoding failed")
        }
        return (scaled.width, scaled.height, jpeg)
    }

    /// Shrinks `image` so that its width is at most `maxWidth`, keeping the aspect ratio.
    /// Returns `image` unchanged when it already fits.
    static func downscale(_ image: CGImage, maxWidth: Int) -> CGImage {
        let width = image.width
        let height = image.height
        guard maxWidth > 0, width > maxWidth, width > 0, height > 0 else { return image }
        let targetWidth = maxWidth
        let targetHeight = max(1, Int((Double(height) * Double(targetWidth) / Double(width)).rounded()))
        let bitmapInfo = CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue
        guard let context = CGContext(data: nil, width: targetWidth, height: targetHeight, bitsPerComponent: 8,
                                      bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                                      bitmapInfo: bitmapInfo) else { return image }
        context.interpolationQuality = .medium
        context.draw(image, in: CGRect(x: 0, y: 0, width: targetWidth, height: targetHeight))
        return context.makeImage() ?? image
    }

    /// JPEG bytes at `quality` (0.1–0.9).
    static func encodeJPEG(_ image: CGImage, quality: Double) -> Data? {
        let representation = NSBitmapImageRep(cgImage: image)
        let factor = min(max(quality, minQuality), maxQuality)
        return representation.representation(using: .jpeg, properties: [.compressionFactor: NSNumber(value: factor)])
    }

    /// The binary WebSocket frame of §4: `u8 0x01, u8 display, u16 w, u16 h (big-endian), JPEG`.
    static func frame(display: Int, width: Int, height: Int, jpeg: Data) -> Data {
        var data = Data(capacity: 6 + jpeg.count)
        data.append(frameKindJPEG)
        data.append(UInt8(truncatingIfNeeded: display))
        data.append(UInt8(truncatingIfNeeded: width >> 8))
        data.append(UInt8(truncatingIfNeeded: width))
        data.append(UInt8(truncatingIfNeeded: height >> 8))
        data.append(UInt8(truncatingIfNeeded: height))
        data.append(jpeg)
        return data
    }
}
