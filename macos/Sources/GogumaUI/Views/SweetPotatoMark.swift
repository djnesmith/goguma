import AppKit

/// The sweet potato mark: Apple's 🍠, rendered from the system emoji font.
///
/// **Not a drawing of the emoji: the emoji.** Four hand-built attempts got
/// progressively closer and none looked right: a leaf, a peanut, a banana, and
/// finally a tuber that was recognisably a tuber and still read as a purple
/// blob at 16pt. Apple already ships artwork that is unmistakable at every size
/// it is used at, so the honest move is to draw that rather than approximate it.
///
/// One of those attempts did come back, in `templateImage` below — but for the
/// menu bar only, and stripped to a single colour on purpose rather than losing
/// its palette by accident. "Reads as a purple blob" was a complaint about a
/// colour drawing competing with the emoji; it is not a complaint about a
/// monochrome silhouette, which is what a status item is supposed to be.
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
/// image and cannot be tinted.
///
/// **That cost is why the menu bar no longer draws it.** A status item is
/// supposed to be a template — macOS discards the colour and tints the alpha to
/// match a light bar, a dark bar and the inverted highlight — and this image
/// cannot be one. So the menu bar draws `templateImage` below instead: the
/// outline, hand-built after all, because at 18pt the shape is the part that
/// identifies a sweet potato and the palette is the part that cannot survive.
/// The emoji is still what every surface *inside* the app uses, where colour is
/// wanted and nothing is being tinted.
///
/// **The glyph does not change with state**, in either form. Fading it at rest
/// was tried and removed: what makes a sweet potato legible at a glance is the
/// yellow cut face against the purple skin, and dimming to 45% washed exactly
/// that away.
///
/// So state lives where it still reads: the popover's headline, the status
/// line, and the hold counter. The menu bar says *which app*, not *what it is
/// doing*. That is a real reduction from the bear, whose eyes carried the state
/// in shape and survived greyscale.
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

    /// The mark as a **template**: one flat silhouette, no colour.
    ///
    /// This is what the menu bar gets. A status item image is meant to be a
    /// template — macOS discards the colour, tints the alpha to match a light
    /// bar or a dark bar, and inverts it while the menu is open. The colour
    /// emoji above cannot be one: its alpha channel is the entire potato, so
    /// handing it over as a template gives a featureless blob carrying none of
    /// what makes the emoji legible, which is worse than either option.
    ///
    /// So the outline is drawn rather than borrowed. At 18pt the emoji's
    /// internal detail — the cut face, the skin — is already doing very little;
    /// what identifies it at that size is the tapered tuber shape, and that is
    /// the part that survives being reduced to one colour.
    ///
    /// State is deliberately not in this glyph, unchanged from the emoji it
    /// replaces: the menu bar says *which app*, and the popover behind it says
    /// what the app is doing, in words.
    static func templateImage(size: CGFloat) -> NSImage {
        let image = NSImage(size: NSSize(width: size, height: size), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else { return false }
            let path = tuber()
            // Fit the ink rather than a nominal box: the shape is rotated, so
            // its bounds are not worth working out by hand, and asking the path
            // stays correct if the numbers below are ever tuned.
            let ink = path.boundingBoxOfPath
            guard ink.width > 0, ink.height > 0 else { return false }
            let scale =
                min(rect.width / ink.width, rect.height / ink.height) * Outline.fill
            ctx.translateBy(
                x: rect.midX - ink.midX * scale,
                y: rect.midY - ink.midY * scale
            )
            ctx.scaleBy(x: scale, y: scale)
            // Black is only the alpha carrier; the system chooses the ink.
            ctx.setFillColor(NSColor.black.cgColor)
            ctx.addPath(path)
            ctx.fillPath()
            return true
        }
        image.isTemplate = true
        return image
    }

    /// The silhouette, in its own 120×120 units.
    ///
    /// **Lobed at both ends, and only moderately tilted.** The first version was
    /// a long shallow body with a single round bulb at one end, tilted 30°.
    /// Rendered at proof size that silhouette is unmistakably phallic — which is
    /// not a thing to discover in a menu bar, in a screen share, permanently.
    /// A real tuber is lumpy at both ends and not much longer than it is wide,
    /// so this is: two unequal lobes, a shorter body, and less lean.
    private enum Outline {
        static let centre = CGPoint(x: 60, y: 60)
        /// Shorter and deeper than a shaft: aspect near 2:1, not 3:1.
        static let bodyRadius = CGSize(width: 36, height: 21)
        /// Both ends carry a lobe. Unequal radii keep it from reading as a
        /// capsule, which is the other failure mode; equal ones made a pill.
        static let lobes: [(centre: CGPoint, radius: CGFloat)] = [
            (CGPoint(x: 84, y: 57), 22),
            (CGPoint(x: 34, y: 64), 17),
        ]
        /// Enough lean to look grown rather than drawn, not enough to read as
        /// a diagonal shaft.
        static let tilt: CGFloat = -.pi / 9
        /// Leaves a margin, so no edge touches the bounds at any size.
        static let fill: CGFloat = 0.94
    }

    /// Body and both lobes as one path, filled non-zero so they union — the
    /// same construction `MenuBarMark` uses for its ears and head.
    private static func tuber() -> CGPath {
        let rotation = CGAffineTransform(
            translationX: Outline.centre.x, y: Outline.centre.y
        )
        .rotated(by: Outline.tilt)
        .translatedBy(x: -Outline.centre.x, y: -Outline.centre.y)

        let path = CGMutablePath()
        path.addEllipse(
            in: CGRect(
                x: Outline.centre.x - Outline.bodyRadius.width,
                y: Outline.centre.y - Outline.bodyRadius.height,
                width: Outline.bodyRadius.width * 2,
                height: Outline.bodyRadius.height * 2
            ),
            transform: rotation
        )
        for lobe in Outline.lobes {
            path.addEllipse(
                in: CGRect(
                    x: lobe.centre.x - lobe.radius,
                    y: lobe.centre.y - lobe.radius,
                    width: lobe.radius * 2,
                    height: lobe.radius * 2
                ),
                transform: rotation
            )
        }
        return path
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
