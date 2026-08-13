import AppKit

/// The sweet potato mark: Apple's 🍠, rendered from the system emoji font.
///
/// **Not a drawing of the emoji: the emoji.** Four hand-built attempts got
/// progressively closer and none looked right: a leaf, a peanut, a banana, and
/// finally a tuber that was recognisably a tuber and still read as a purple
/// blob at 16pt. Apple already ships artwork that is unmistakable at every size
/// it is used at, so the honest move is to draw that rather than approximate it.
///
/// **Rendered once large, then scaled, never drawn at the requested size.**
/// Apple Color Emoji is a bitmap font with fixed strikes, so its metrics are
/// not proportional: ink measured at 128pt does not predict ink at 16pt. Every
/// attempt to compute an offset from one size and draw at another put the
/// artwork against an edge, which is why the right side kept clipping at menu
/// bar sizes while looking perfect in a proof sheet. Cropping the artwork once
/// and scaling the bitmap sidesteps the metrics entirely.
///
/// The cost, stated plainly: this is full colour, so it cannot be a template
/// image and cannot be tinted. The menu bar glyph therefore ignores the state
/// colour entirely, and `MenuBarIcon`'s ember/brand tinting does nothing to it.
/// That is the trade for looking exactly like the emoji.
///
/// **The glyph does not change with state.** Fading it at rest was tried and
/// removed: the thing that makes a sweet potato legible at a glance is the
/// yellow cut face against the purple skin, and dimming to 45% washed exactly
/// that away; the mark stopped reading as a sweet potato at all, which is a
/// worse loss than having no state in the glyph.
///
/// So state lives where it still reads: the popover's headline, the status
/// line, and the hold counter. The menu bar says *which app*, not *what it is
/// doing*. That is a real reduction from the bear, whose eyes carried the
/// state in shape and survived greyscale, and it is the price of using the
/// emoji as-is.
enum SweetPotatoMark {
    /// The glyph itself: a roasted sweet potato, purple skin, yellow flesh.
    private static let glyph = "\u{1F360}"

    /// Fraction of the frame the artwork occupies, leaving a margin so no edge
    /// can touch the bounds at any size.
    private static let fill: CGFloat = 0.88

    /// - Parameters:
    ///   - asleep: accepted so call sites match the drawn marks, and ignored:
    ///     the glyph is identical in both states.
    ///   - skin: ignored. A colour emoji carries its own palette.
    ///   - flesh: ignored, as above.
    @MainActor
    static func image(
        size: CGFloat, asleep: Bool, skin: NSColor? = nil, flesh: NSColor? = nil
    ) -> NSImage {
        _ = skin
        _ = flesh

        guard let art = artwork() else {
            // No usable glyph: fall back rather than return a blank image,
            // which in a menu bar is an invisible, unclickable status item.
            return MenuBarMark.image(size: size, asleep: asleep)
        }

        let aspect = CGFloat(art.width) / CGFloat(art.height)
        var drawn = CGSize(width: size * fill, height: size * fill)
        if aspect > 1 {
            drawn.height = drawn.width / aspect
        } else {
            drawn.width = drawn.height * aspect
        }

        let image = NSImage(size: NSSize(width: size, height: size), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else { return false }
            ctx.interpolationQuality = .high
            ctx.draw(art, in: CGRect(
                x: rect.midX - drawn.width / 2,
                y: rect.midY - drawn.height / 2,
                width: drawn.width,
                height: drawn.height
            ))
            return true
        }
        image.isTemplate = false
        return image
    }

    @MainActor private static var cachedArt: CGImage?

    /// The glyph rendered large and cropped to its ink, once.
    ///
    /// Large because a bitmap font's biggest strike is its best one, and
    /// scaling down is lossless-looking while scaling up is not. Cropped
    /// because the layout box is mostly bearing, and unevenly so.
    @MainActor
    private static func artwork() -> CGImage? {
        if let cachedArt { return cachedArt }

        let point: CGFloat = 256
        let text = NSAttributedString(
            string: glyph, attributes: [.font: NSFont.systemFont(ofSize: point)]
        )
        let box = text.size()
        // Generous canvas: a glyph may overhang its own advance, and a probe
        // that clips is a measurement that lies.
        let canvas = NSSize(width: box.width * 2, height: box.height * 2)
        guard canvas.width > 2, canvas.height > 2,
              let rep = NSBitmapImageRep(
                  bitmapDataPlanes: nil,
                  pixelsWide: Int(canvas.width.rounded(.up)),
                  pixelsHigh: Int(canvas.height.rounded(.up)),
                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
              )
        else { return nil }

        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
        text.draw(at: NSPoint(x: box.width / 2, y: box.height / 2))
        NSGraphicsContext.restoreGraphicsState()

        var minX = rep.pixelsWide, maxX = -1
        var minY = rep.pixelsHigh, maxY = -1
        for y in 0..<rep.pixelsHigh {
            for x in 0..<rep.pixelsWide where (rep.colorAt(x: x, y: y)?.alphaComponent ?? 0) > 0.02 {
                minX = min(minX, x); maxX = max(maxX, x)
                minY = min(minY, y); maxY = max(maxY, y)
            }
        }
        guard maxX >= minX, maxY >= minY, let full = rep.cgImage else { return nil }

        cachedArt = full.cropping(to: CGRect(
            x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1
        ))
        return cachedArt
    }
}
