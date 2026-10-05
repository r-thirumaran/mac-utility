// Generates Clipwell's app icon and writes a full .iconset.
//
//     swift Tools/make_icon.swift
//
// Design: a stack of rounded cards — the clipboard history — where the front card is split
// into two halves, which is the window-snapping half of the app. Both features in one mark.
//
// Everything is drawn with CoreGraphics rather than shipped as binary art, so the icon is
// diffable, tweakable, and regenerates identically. No external tools, no design app.
//
// Layout follows Apple's macOS icon grid: the rounded square occupies 824pt of a 1024pt
// canvas, centred, leaving the transparent margin the system expects. Getting this wrong is
// why home-made Mac icons look subtly too big next to Apple's.

import AppKit
import ImageIO
import UniformTypeIdentifiers
import CoreGraphics
import Foundation

let canvas: CGFloat = 1024
let plate: CGFloat = 824                       // the rounded square itself
let plateOrigin = (canvas - plate) / 2
let plateRadius: CGFloat = plate * 0.2237      // Apple's squircle-ish corner ratio

func makeImage() -> CGImage {
    let space = CGColorSpaceCreateDeviceRGB()
    guard let ctx = CGContext(data: nil,
                              width: Int(canvas), height: Int(canvas),
                              bitsPerComponent: 8, bytesPerRow: 0,
                              space: space,
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
    else { fatalError("could not create bitmap context") }

    ctx.setAllowsAntialiasing(true)
    ctx.interpolationQuality = .high

    let plateRect = CGRect(x: plateOrigin, y: plateOrigin, width: plate, height: plate)
    let platePath = CGPath(roundedRect: plateRect,
                           cornerWidth: plateRadius, cornerHeight: plateRadius,
                           transform: nil)

    // --- background: indigo -> blue, top-left to bottom-right -----------------------------
    ctx.saveGState()
    ctx.addPath(platePath)
    ctx.clip()
    let bg = CGGradient(colorsSpace: space,
                        colors: [
                            CGColor(red: 0.36, green: 0.32, blue: 0.86, alpha: 1),
                            CGColor(red: 0.16, green: 0.44, blue: 0.90, alpha: 1),
                        ] as CFArray,
                        locations: [0, 1])!
    ctx.drawLinearGradient(bg,
                           start: CGPoint(x: plateRect.minX, y: plateRect.maxY),
                           end: CGPoint(x: plateRect.maxX, y: plateRect.minY),
                           options: [])

    // A soft highlight across the top third stops the plate reading as flat colour.
    let sheen = CGGradient(colorsSpace: space,
                           colors: [
                               CGColor(red: 1, green: 1, blue: 1, alpha: 0.18),
                               CGColor(red: 1, green: 1, blue: 1, alpha: 0),
                           ] as CFArray,
                           locations: [0, 1])!
    ctx.drawLinearGradient(sheen,
                           start: CGPoint(x: plateRect.midX, y: plateRect.maxY),
                           end: CGPoint(x: plateRect.midX, y: plateRect.midY),
                           options: [])
    ctx.restoreGState()

    // --- the card stack --------------------------------------------------------------------
    // Two cards behind the front pair, each narrower and higher, so the mark reads as a stack
    // of clippings. Kept opaque-ish rather than faint: at 16pt anything under ~0.45 alpha
    // dissolves into the gradient and the depth cue is lost entirely.
    let cardWidth: CGFloat = 520
    let cardHeight: CGFloat = 250
    let cardRadius: CGFloat = 34
    let centreX = canvas / 2
    let baseY: CGFloat = 300                    // bottom edge of the front pair
    let gap: CGFloat = 34

    // The split runs through the *entire* stack, not just the front pair. Without this the
    // back cards show through the gap as a muddy vertical stripe and the split stops reading
    // as a split at all. Clipped with the even-odd rule: plate minus the gap column.
    let gapColumn = CGRect(x: centreX - gap / 2, y: 0, width: gap, height: canvas)
    ctx.saveGState()
    let mask = CGMutablePath()
    mask.addRect(CGRect(x: 0, y: 0, width: canvas, height: canvas))
    mask.addRect(gapColumn)
    ctx.addPath(mask)
    ctx.clip(using: .evenOdd)

    struct Card { let inset: CGFloat; let lift: CGFloat; let alpha: CGFloat }
    for card in [Card(inset: 150, lift: 156, alpha: 0.46),   // furthest back
                 Card(inset: 76,  lift: 80,  alpha: 0.70)] {
        let rect = CGRect(x: centreX - (cardWidth - card.inset) / 2,
                          y: baseY + card.lift,
                          width: cardWidth - card.inset,
                          height: cardHeight)
        ctx.setFillColor(CGColor(red: 1, green: 1, blue: 1, alpha: card.alpha))
        ctx.addPath(CGPath(roundedRect: rect,
                           cornerWidth: cardRadius, cornerHeight: cardRadius, transform: nil))
        ctx.fillPath()
    }
    ctx.restoreGState()

    // --- front: two cards side by side = a window snapped into halves ----------------------
    //
    // Drawn as two separate rounded rects with the gradient showing between them, NOT as one
    // card with a slot cut out of it. Clearing a gap punches a hole straight through the plate
    // to the transparent canvas, which renders as a black bar over any dark backdrop.
    let halfWidth = (cardWidth - gap) / 2

    let leftHalf = CGRect(x: centreX - cardWidth / 2, y: baseY,
                          width: halfWidth, height: cardHeight)
    let rightHalf = CGRect(x: centreX + gap / 2, y: baseY,
                           width: halfWidth, height: cardHeight)

    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -12), blur: 30,
                  color: CGColor(red: 0, green: 0, blue: 0, alpha: 0.30))

    // Left is pure white, right is tinted: two panes, clearly distinct, and the asymmetry
    // survives being shrunk to a menu-bar-sized thumbnail.
    ctx.setFillColor(CGColor(red: 1, green: 1, blue: 1, alpha: 1))
    ctx.addPath(CGPath(roundedRect: leftHalf,
                       cornerWidth: cardRadius, cornerHeight: cardRadius, transform: nil))
    ctx.fillPath()

    ctx.setFillColor(CGColor(red: 0.70, green: 0.79, blue: 1.0, alpha: 1))
    ctx.addPath(CGPath(roundedRect: rightHalf,
                       cornerWidth: cardRadius, cornerHeight: cardRadius, transform: nil))
    ctx.fillPath()
    ctx.restoreGState()

    guard let image = ctx.makeImage() else { fatalError("could not render icon") }
    return image
}

// MARK: - Write the iconset

let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
let iconset = root.appendingPathComponent("build/AppIcon.iconset")
try? FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)

let master = makeImage()

/// (filename, pixel size)
let variants: [(String, Int)] = [
    ("icon_16x16.png", 16),      ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32),      ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128),   ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256),   ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512),   ("icon_512x512@2x.png", 1024),
]

func scaled(_ image: CGImage, to size: Int) -> CGImage {
    guard let ctx = CGContext(data: nil, width: size, height: size,
                              bitsPerComponent: 8, bytesPerRow: 0,
                              space: CGColorSpaceCreateDeviceRGB(),
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
    else { fatalError("scale context failed") }
    ctx.interpolationQuality = .high
    ctx.draw(image, in: CGRect(x: 0, y: 0, width: size, height: size))
    return ctx.makeImage()!
}

func write(_ image: CGImage, to url: URL) {
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil)
    else { fatalError("could not write \(url.lastPathComponent)") }
    CGImageDestinationAddImage(dest, image, nil)
    CGImageDestinationFinalize(dest)
}

for (name, size) in variants {
    write(scaled(master, to: size), to: iconset.appendingPathComponent(name))
}

// A standalone 1024 for the README and the GitHub social preview.
let assets = root.appendingPathComponent("assets")
try? FileManager.default.createDirectory(at: assets, withIntermediateDirectories: true)
write(master, to: assets.appendingPathComponent("icon.png"))

print("wrote \(variants.count) sizes to build/AppIcon.iconset and assets/icon.png")

// MARK: - Social preview card
//
// GitHub renders a 1280x640 image whenever the repo is linked on Slack, X, LinkedIn or in
// search results. Without one the link is a wall of grey text, which is the single biggest
// difference between a repo that gets clicked and one that does not.

func makeSocialCard() -> CGImage {
    let w = 1280, h = 640
    let space = CGColorSpaceCreateDeviceRGB()
    guard let ctx = CGContext(data: nil, width: w, height: h,
                              bitsPerComponent: 8, bytesPerRow: 0, space: space,
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
    else { fatalError("card context failed") }

    // Same gradient as the icon so the two read as one identity.
    let bg = CGGradient(colorsSpace: space,
                        colors: [
                            CGColor(red: 0.13, green: 0.12, blue: 0.28, alpha: 1),
                            CGColor(red: 0.10, green: 0.18, blue: 0.38, alpha: 1),
                        ] as CFArray, locations: [0, 1])!
    ctx.drawLinearGradient(bg, start: CGPoint(x: 0, y: CGFloat(h)),
                           end: CGPoint(x: CGFloat(w), y: 0), options: [])

    // Icon on the left.
    let iconSize: CGFloat = 300
    ctx.draw(master, in: CGRect(x: 96, y: CGFloat(h)/2 - iconSize/2,
                                width: iconSize, height: iconSize))

    // Text on the right, drawn through AppKit so we get real font metrics.
    let gfx = NSGraphicsContext(cgContext: ctx, flipped: false)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = gfx

    func draw(_ text: String, x: CGFloat, y: CGFloat, size: CGFloat,
              weight: NSFont.Weight, alpha: CGFloat) {
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: size, weight: weight),
            .foregroundColor: NSColor(white: 1, alpha: alpha),
        ]
        NSAttributedString(string: text, attributes: attrs)
            .draw(at: NSPoint(x: x, y: y))
    }

    let left: CGFloat = 452
    draw("Clipwell", x: left, y: 372, size: 88, weight: .bold, alpha: 1.0)
    draw("Window snapping + clipboard history for macOS",
         x: left, y: 314, size: 32, weight: .medium, alpha: 0.88)
    draw("Aero Snap and Win+V, on the Mac.",
         x: left, y: 254, size: 26, weight: .regular, alpha: 0.62)
    draw("Free  ·  open source  ·  no dependencies",
         x: left, y: 196, size: 24, weight: .regular, alpha: 0.52)

    NSGraphicsContext.restoreGraphicsState()
    return ctx.makeImage()!
}

write(makeSocialCard(), to: assets.appendingPathComponent("social-card.png"))
print("wrote assets/social-card.png (1280x640)")
