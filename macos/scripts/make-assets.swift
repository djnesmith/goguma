import AppKit

// Generates the two pieces of artwork the installer needs: the app icon, and
// the disk image background.
//
// Both are built from Apple's 🍠 glyph, the same source `SweetPotatoMark` uses
// in the menu bar and `site/build-potato.swift` uses for the favicon. Drawing
// the mark three different ways would give three subtly different potatoes.
//
// Usage:  swift make-assets.swift <output-dir>

// MARK: - Palette
//
// Read off Theme.Colors, light mode. Kept as literals rather than imported
// because this runs as a standalone script with no access to the app target.

let surface = NSColor(srgbRed: 0xF4 / 255, green: 0xF0 / 255, blue: 0xF8 / 255, alpha: 1)
let potatoSkin = NSColor(srgbRed: 0x8E / 255, green: 0x5F / 255, blue: 0xA8 / 255, alpha: 1)
let accentDeep = NSColor(srgbRed: 0x6D / 255, green: 0x4A / 255, blue: 0x86 / 255, alpha: 1)
let textPrimary = NSColor(srgbRed: 0x24 / 255, green: 0x1C / 255, blue: 0x2A / 255, alpha: 1)

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."

// MARK: - Helpers

func bitmap(_ size: NSSize, _ draw: (CGContext) -> Void) -> NSBitmapImageRep {
    guard
        let rep = NSBitmapImageRep(
            bitmapDataPlanes: nil,
            pixelsWide: Int(size.width), pixelsHigh: Int(size.height),
            bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
            colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)
    else { fatalError("could not allocate \(size)") }
    rep.size = size
    NSGraphicsContext.saveGraphicsState()
    let ctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = ctx
    draw(ctx.cgContext)
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

func write(_ rep: NSBitmapImageRep, to path: String) {
    guard let data = rep.representation(using: .png, properties: [:]) else {
        fatalError("could not encode \(path)")
    }
    try! data.write(to: URL(fileURLWithPath: path))
}

/// The 🍠 glyph as an image cropped to its ink.
///
/// Apple Color Emoji is a bitmap font with fixed strikes, so its metrics are not
/// proportional: ink measured at one point size does not predict ink at another.
/// Rendered once, large, then cropped, which is what build-potato.swift does.
func potatoImage() -> NSImage {
    let text = NSAttributedString(
        string: "\u{1F360}", attributes: [.font: NSFont.systemFont(ofSize: 512)])
    let box = text.size()
    let canvas = NSSize(width: (box.width * 2).rounded(.up), height: (box.height * 2).rounded(.up))
    let rep = bitmap(canvas) { _ in
        text.draw(at: NSPoint(x: box.width / 2, y: box.height / 2))
    }

    var minX = rep.pixelsWide, maxX = -1, minY = rep.pixelsHigh, maxY = -1
    for y in 0..<rep.pixelsHigh {
        for x in 0..<rep.pixelsWide {
            guard let c = rep.colorAt(x: x, y: y), c.alphaComponent > 0.02 else { continue }
            minX = min(minX, x); maxX = max(maxX, x)
            minY = min(minY, y); maxY = max(maxY, y)
        }
    }
    guard maxX > minX else { fatalError("the emoji rendered blank") }

    let cropped = NSImage(size: NSSize(width: maxX - minX + 1, height: maxY - minY + 1))
    cropped.addRepresentation(rep)
    let ink = NSRect(
        x: minX, y: rep.pixelsHigh - maxY - 1,
        width: maxX - minX + 1, height: maxY - minY + 1)
    let out = NSImage(size: ink.size)
    out.lockFocus()
    NSImage(size: NSSize(width: rep.pixelsWide, height: rep.pixelsHigh), flipped: false) { r in
        rep.draw(in: r); return true
    }.draw(
        in: NSRect(origin: .zero, size: ink.size), from: ink, operation: .sourceOver, fraction: 1)
    out.unlockFocus()
    return out
}

let potato = potatoImage()

// MARK: - App icon
//
// macOS icons since Big Sur are a squircle at a fixed proportion of the canvas,
// with the artwork inset inside that. The numbers below are Apple's grid: the
// shape occupies 824/1024 of the tile, and the corner radius is ~22.37% of the
// shape. Getting this wrong is the single most obvious way an icon reads as
// third-party next to the system's own.

func iconTile(_ px: CGFloat) -> NSBitmapImageRep {
    bitmap(NSSize(width: px, height: px)) { ctx in
        let scale = px / 1024
        let shape = NSRect(x: 100 * scale, y: 100 * scale, width: 824 * scale, height: 824 * scale)
        let radius = shape.width * 0.2237

        // A soft vertical gradient rather than a flat fill: flat tiles read as
        // placeholder art at large sizes, where this icon is mostly seen.
        let path = NSBezierPath(roundedRect: shape, xRadius: radius, yRadius: radius)
        ctx.saveGState()
        path.addClip()
        let gradient = NSGradient(
            colors: [
                potatoSkin.blended(withFraction: 0.18, of: .white)!,
                accentDeep,
            ])!
        gradient.draw(in: shape, angle: -90)
        ctx.restoreGState()

        // The mark, inset so it sits inside the shape's optical margin rather
        // than crowding its corners.
        let inset = shape.insetBy(dx: shape.width * 0.19, dy: shape.height * 0.19)
        let ratio = potato.size.width / potato.size.height
        var art = inset
        if ratio > 1 {
            art.size.height = inset.width / ratio
            art.origin.y += (inset.height - art.height) / 2
        } else {
            art.size.width = inset.height * ratio
            art.origin.x += (inset.width - art.width) / 2
        }
        potato.draw(in: art, from: .zero, operation: .sourceOver, fraction: 1)
    }
}

let iconset = outDir + "/goguma.iconset"
try? FileManager.default.createDirectory(
    atPath: iconset, withIntermediateDirectories: true)
for (px, name) in [
    (16, "icon_16x16"), (32, "icon_16x16@2x"), (32, "icon_32x32"), (64, "icon_32x32@2x"),
    (128, "icon_128x128"), (256, "icon_128x128@2x"), (256, "icon_256x256"),
    (512, "icon_256x256@2x"), (512, "icon_512x512"), (1024, "icon_512x512@2x"),
] {
    write(iconTile(CGFloat(px)), to: "\(iconset)/\(name).png")
}
print("wrote \(iconset)")

// MARK: - Disk image background
//
// Drawn at 2x and shown in a half-size window, so it stays crisp on Retina;
// Finder scales a background to fit the window either way.
//
// The layout mirrors what every well-made Mac installer does, because the
// convention is the instruction: a sentence at the top, the app on the left,
// the Applications folder on the right, and an arrow making the verb literal.
// Finder draws the two icons itself at the positions set in make-dmg.sh; this
// image supplies everything behind them.

// Icon centres, in window points from the top-left. make-dmg.sh sets Finder to
// exactly these, and the arrow below is aimed between them, so the two files
// have to be changed together.
let winW: CGFloat = 660, winH: CGFloat = 420
let appX: CGFloat = 165, appY: CGFloat = 268
let appsX: CGFloat = 495, appsY: CGFloat = 268

let bg = bitmap(NSSize(width: winW * 2, height: winH * 2)) { ctx in
    ctx.scaleBy(x: 2, y: 2)

    surface.setFill()
    NSRect(x: 0, y: 0, width: winW, height: winH).fill()

    // Headline. Serif, because the palette is warm and a grotesque here reads
    // as a system alert rather than as a greeting. "drag" is italic for the
    // same reason Wispr's is: it is the one word that is an instruction.
    let serif = NSFontDescriptor.preferredFontDescriptor(
        forTextStyle: .largeTitle
    ).withDesign(.serif)!
    let regular = NSFont(descriptor: serif, size: 34)!
    let italic = NSFont(
        descriptor: serif.withSymbolicTraits(.italic), size: 34)!

    let line = NSMutableAttributedString()
    let base: [NSAttributedString.Key: Any] = [.font: regular, .foregroundColor: textPrimary]
    line.append(NSAttributedString(string: "To install, ", attributes: base))
    line.append(
        NSAttributedString(
            string: "drag",
            attributes: [.font: italic, .foregroundColor: textPrimary]))
    line.append(NSAttributedString(string: " goguma to Applications", attributes: base))

    let paragraph = NSMutableParagraphStyle()
    paragraph.alignment = .center
    line.addAttribute(
        .paragraphStyle, value: paragraph, range: NSRange(location: 0, length: line.length))

    // Flipped into Cocoa's bottom-left origin: the headline sits in the top
    // third, above the icon row.
    let textRect = NSRect(x: 40, y: winH - 130, width: winW - 80, height: 90)
    line.draw(in: textRect)

    // The arrow, from the app toward Applications. Hand-drawn rather than a
    // glyph: a straight system arrow between two icons reads as a diagram,
    // and this is meant to read as someone showing you.
    //
    // One arc that rises and comes back down, not an S. The first attempt used
    // control points on opposite sides of the line, which averages out to a
    // flat squiggle: too little amplitude to read as a gesture, and no clear
    // direction. Both control points above the baseline give a single confident
    // sweep that lands on the folder.
    //
    // Spans the gap between the icon boxes, not between their centres, or it
    // disappears underneath them. Icons are 128pt wide, so their facing edges
    // are at appX+64 and appsX-64; this insets a further 21pt for air.
    // The arrow is vendored artwork, not drawn here: see assets/ATTRIBUTION.md,
    // including the unresolved licence question. It is authored pointing up and
    // to the right, so it is rotated flat into the gap between the two icons.
    //
    // Recoloured by substituting the fill in the SVG source rather than tinting
    // the rendered bitmap: tinting repaints every pixel the shape covers,
    // including its antialiased edges, which leaves a hard fringe.
    let svgPath = FileManager.default.fileExists(atPath: "assets/arrow.svg")
        ? "assets/arrow.svg" : outDir + "/arrow.svg"
    if var svg = try? String(contentsOfFile: svgPath, encoding: .utf8) {
        func hex(_ c: NSColor) -> String {
            let s = c.usingColorSpace(.sRGB)!
            return String(
                format: "#%02X%02X%02X", Int(s.redComponent * 255),
                Int(s.greenComponent * 255), Int(s.blueComponent * 255))
        }
        svg = svg.replacingOccurrences(of: "CURRENT", with: hex(potatoSkin))
        if let img = NSImage(data: Data(svg.utf8)) {
            let side: CGFloat = 116
            let cx = (appX + appsX) / 2 - 8
            let cy = winH - appY - 6

            ctx.saveGState()
            ctx.translateBy(x: cx, y: cy)
            // Flattens the diagonal into a drag that reads left to right, with
            // a little downward run-out so it lands on the folder rather than
            // pointing past it.
            ctx.rotate(by: -46 * .pi / 180)
            img.draw(
                in: NSRect(x: -side / 2, y: -side / 2, width: side, height: side),
                from: .zero, operation: .sourceOver, fraction: 1)
            ctx.restoreGState()
        }
    }
}
// Written as 1320x840 pixels declaring 660x420 points, i.e. 144 dpi.
//
// This is the whole ballgame for a Retina background. Finder lays the image out
// in POINTS, so a 1320x840 image that declares 72 dpi is treated as a 1320x840
// point background and a 660x420 window shows its top-left quarter: headline
// clipped, arrow off-screen, everything apparently broken. Declaring 144 dpi
// makes the same pixels describe a 660x420 point image that happens to be
// double resolution, which is exactly what is wanted.
bg.size = NSSize(width: winW, height: winH)
write(bg, to: outDir + "/dmg-background.png")
print("wrote \(outDir)/dmg-background.png  (1320x840px @ 660x420pt)")

// MARK: - Layout preview
//
// Composites what Finder will actually show: the background at its true point
// size, with the two icons drawn at the positions make-dmg.sh sets. Exists
// because the DMG window itself cannot be screenshotted from a script, so
// without this the layout can only be checked by building an image, mounting
// it, and looking — a slow loop that hides mistakes like the one above.
func systemIcon(_ path: String, size: CGFloat) -> NSImage {
    let img = NSWorkspace.shared.icon(forFile: path)
    img.size = NSSize(width: size, height: size)
    return img
}

let preview = bitmap(NSSize(width: winW * 2, height: winH * 2)) { ctx in
    ctx.scaleBy(x: 2, y: 2)
    NSImage(size: NSSize(width: winW, height: winH), flipped: false) { r in
        bg.draw(in: r); return true
    }.draw(in: NSRect(x: 0, y: 0, width: winW, height: winH))

    let iconSide: CGFloat = 128
    func place(_ image: NSImage, _ label: String, cx: CGFloat, cy: CGFloat) {
        // Finder centres the icon box on the position and hangs the label
        // beneath it; cy is measured from the top, so flip it.
        let box = NSRect(
            x: cx - iconSide / 2, y: winH - cy - iconSide / 2,
            width: iconSide, height: iconSide)
        image.draw(in: box, from: .zero, operation: .sourceOver, fraction: 1)

        let para = NSMutableParagraphStyle()
        para.alignment = .center
        NSAttributedString(
            string: label,
            attributes: [
                .font: NSFont.systemFont(ofSize: 13),
                .foregroundColor: textPrimary,
                .paragraphStyle: para,
            ]
        ).draw(in: NSRect(x: cx - 90, y: box.minY - 22, width: 180, height: 18))
    }

    let appIcon = NSImage(size: NSSize(width: 512, height: 512))
    appIcon.addRepresentation(iconTile(512))
    place(appIcon, "goguma", cx: appX, cy: appY)
    place(systemIcon("/Applications", size: iconSide), "Applications", cx: appsX, cy: appsY)
}
preview.size = NSSize(width: winW, height: winH)
write(preview, to: outDir + "/dmg-preview.png")
print("wrote \(outDir)/dmg-preview.png  (what the window should look like)")
