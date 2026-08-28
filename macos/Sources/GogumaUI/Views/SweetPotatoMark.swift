import AppKit

/// The sweet potato mark: Apple's 🍠, rendered from the system emoji font.
///
/// **Not a drawing of the emoji: the emoji.** Four hand-built attempts got
/// progressively closer and none looked right: a leaf, a peanut, a banana, and
/// finally a tuber that was recognisably a tuber and still read as a purple
/// blob at 16pt. Apple already ships artwork that is unmistakable at every size
/// it is used at, so the honest move is to draw that rather than approximate it.
///
/// One of those attempts did come back, in `outlineImage` below — but for the
/// menu bar only, and drawn as an outline rather than a filled shape. "Reads as
/// a purple blob" was a complaint about a colour drawing competing with the
/// emoji, and a filled monochrome shape has the same problem for a different
/// reason: it has an edge and nothing else. An outline has an interior, and the
/// interior is where the surface marks go.
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
/// normally a template — macOS discards the colour and tints the alpha to match
/// a light bar, a dark bar and the inverted highlight — and this image cannot
/// be one. So the menu bar draws `outlineImage` below instead: the outline,
/// hand-built after all, because at 18pt the shape is the part that identifies
/// a sweet potato and the palette is the part that cannot survive. The emoji is
/// still what every surface *inside* the app uses, where colour is wanted and
/// nothing is being tinted.
///
/// **The emoji does not change with state.** Fading it at rest was tried and
/// removed: what makes a sweet potato legible at a glance is the yellow cut
/// face against the purple skin, and dimming to 45% washed exactly that away.
///
/// `outlineImage` does carry state, by colour rather than by shape: it is a
/// template at rest and a coloured non-template while sleep is being held off.
/// That is the one thing worth knowing without opening anything — whether the
/// Mac can sleep right now — and it is the only state in the glyph. Everything
/// else is in the popover, in words.
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

    /// The mark for the menu bar: a stroked outline, hollow, with surface
    /// marks — and coloured or not depending on what the Mac is doing.
    ///
    /// - Parameter colour: `nil` produces a **template**, which macOS tints
    ///   itself to match a light bar, a dark bar and the inverted highlight.
    ///   That is the resting state. Pass a colour to get a non-template image
    ///   that keeps it, which is how "holding sleep off" is shown: a template
    ///   cannot carry colour, so the state has to be painted in.
    ///
    /// **Why an outline and not the filled shape it replaced.** The filled
    /// version was a solid lozenge, and at menu bar size a solid lozenge is a
    /// blob: it has an edge and nothing else, so it says "something is here"
    /// and stops. An outline has an interior, and the interior is where the
    /// surface marks live; those marks are most of what makes a sweet potato
    /// read as a sweet potato rather than as a bean, a leaf or a stone.
    ///
    /// The shape is two arcs meeting at two tips — the same construction as the
    /// drawing this is taken from — rather than an ellipse, because a leaf has
    /// points at both ends and an ellipse never does.
    ///
    /// Stroke weight and mark count are chosen from the requested size, not
    /// scaled from one drawing. A stroke that looks correct at 96pt is a hairline
    /// at 18 and disappears into the bar, and five marks inside an 18pt outline
    /// collapse into a smudge. Small gets a heavier stroke and three marks; the
    /// proof sheet renders both so the difference is visible rather than
    /// asserted.
    static func outlineImage(size: CGFloat, colour: NSColor? = nil) -> NSImage {
        let image = NSImage(size: NSSize(width: size, height: size), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else { return false }
            let small = size < Outline.smallBelow
            let stroke = Outline.stroke(small: small)

            let body = leaf(small: small)
            // The stroke straddles the path, so the ink is half a stroke wider
            // than the geometry on every side. Fitting the geometry alone
            // clipped the outline at the tips.
            let ink = body.boundingBoxOfPath.insetBy(dx: -stroke / 2, dy: -stroke / 2)
            guard ink.width > 0, ink.height > 0 else { return false }
            let scale = min(rect.width / ink.width, rect.height / ink.height) * Outline.fill
            ctx.translateBy(
                x: rect.midX - ink.midX * scale,
                y: rect.midY - ink.midY * scale
            )
            ctx.scaleBy(x: scale, y: scale)

            // Black is only the alpha carrier for a template; for the holding
            // state this is the real ink.
            ctx.setStrokeColor((colour ?? .black).cgColor)
            ctx.setLineWidth(stroke)
            // Round joins and caps: the tips meet at an angle sharp enough that
            // a miter join spikes past them, and the marks are short enough
            // that butt caps make them read as scratches rather than marks.
            ctx.setLineJoin(.round)
            ctx.setLineCap(.round)

            ctx.addPath(body)
            ctx.strokePath()
            ctx.addPath(marks(small: small))
            ctx.strokePath()
            return true
        }
        image.isTemplate = colour == nil
        return image
    }

    /// The outline, in its own 120×120 units.
    ///
    /// Two drawings, not one drawing scaled. Below `smallBelow` every
    /// proportion changes, because the small one is not a shrunk version of the
    /// large one and cannot be: at 18pt a stroke that looks right at 96 closes
    /// the interior, and a mark shorter than the stroke is wide renders as a
    /// dot, since round caps on a segment shorter than the line width *are* a
    /// circle. The first attempt shipped exactly that — three dots in a blob.
    private enum Outline {
        /// The two tips, lower-left and upper-right.
        static let tipA = CGPoint(x: 20, y: 26)
        static let tipB = CGPoint(x: 100, y: 94)

        /// Below this point size the small drawing is used. 24pt sits above the
        /// 18pt the menu bar asks for and well below the proof sheet's 96.
        static let smallBelow: CGFloat = 24

        /// How far each arc bows from the line between the tips. The small
        /// drawing is fatter, to leave an interior worth marking after its
        /// heavier stroke has eaten into both sides.
        static func bow(small: Bool) -> CGFloat { small ? 30 : 25 }

        /// Heavier at small size in absolute terms, lighter relative to the
        /// body it encloses.
        static func stroke(small: Bool) -> CGFloat { small ? 8 : 7 }

        /// Marks are longer at small size, not shorter. What has to stay true
        /// is that a mark reads as a line and not a dot, and that is a ratio
        /// against the stroke, not an absolute length.
        static func markLength(small: Bool) -> CGFloat { small ? 16 : 9 }

        /// Three marks at small size: five inside an 18pt outline merge.
        static func markCount(small: Bool) -> Int { small ? 3 : 5 }

        /// Leaves a margin, so no edge touches the bounds at any size.
        static let fill: CGFloat = 0.94
    }

    /// The closed contour: two arcs, tip to tip.
    ///
    /// Symmetric, where the drawing is slightly heavier at the lower tip. At
    /// 18pt that asymmetry is under half a pixel, and symmetry is what keeps the
    /// two arcs from needing four hand-tuned control points instead of two.
    private static func leaf(small: Bool) -> CGPath {
        let a = Outline.tipA
        let b = Outline.tipB
        let along = CGVector(dx: b.x - a.x, dy: b.y - a.y)
        let length = (along.dx * along.dx + along.dy * along.dy).squareRoot()
        // Unit normal to the tip-to-tip line; the arcs bow along ±this.
        let normal = CGVector(dx: -along.dy / length, dy: along.dx / length)
        // A cubic bows to about three quarters of its control offset, so the
        // offset is the wanted bow scaled back up.
        let offset = Outline.bow(small: small) * 4 / 3

        func control(_ t: CGFloat, _ side: CGFloat) -> CGPoint {
            CGPoint(
                x: a.x + along.dx * t + normal.dx * offset * side,
                y: a.y + along.dy * t + normal.dy * offset * side
            )
        }

        let path = CGMutablePath()
        path.move(to: a)
        path.addCurve(to: b, control1: control(1.0 / 3, 1), control2: control(2.0 / 3, 1))
        path.addCurve(to: a, control1: control(2.0 / 3, -1), control2: control(1.0 / 3, -1))
        path.closeSubpath()
        return path
    }

    /// The surface marks: short strokes across the body, not along it.
    ///
    /// Perpendicular to the tip-to-tip line, which is what they are in the
    /// drawing and is also the only orientation that reads at 18pt — marks
    /// running the long way merge with the outline they sit inside.
    ///
    /// Scattered off the centre line rather than strung along it, so they read
    /// as markings on a surface instead of as a seam down the middle.
    private static func marks(small: Bool) -> CGPath {
        let a = Outline.tipA
        let b = Outline.tipB
        let along = CGVector(dx: b.x - a.x, dy: b.y - a.y)
        let length = (along.dx * along.dx + along.dy * along.dy).squareRoot()
        let unit = CGVector(dx: along.dx / length, dy: along.dy / length)
        let normal = CGVector(dx: -unit.dy, dy: unit.dx)

        // Position along the body, and how far off the centre line, for each
        // mark. Five for the large drawing; the small one takes the middle
        // three, which keeps the scatter rather than thinning it to a line.
        let placements: [(t: CGFloat, offset: CGFloat)] = [
            (0.28, 2), (0.41, -6), (0.54, 5), (0.67, -4), (0.78, 1),
        ]
        let count = Outline.markCount(small: small)
        let chosen = count >= placements.count
            ? placements
            : Array(placements[1..<(1 + count)])

        let path = CGMutablePath()
        for place in chosen {
            let centre = CGPoint(
                x: a.x + along.dx * place.t + normal.dx * place.offset,
                y: a.y + along.dy * place.t + normal.dy * place.offset
            )
            let half = Outline.markLength(small: small) / 2
            path.move(to: CGPoint(
                x: centre.x - normal.dx * half, y: centre.y - normal.dy * half))
            path.addLine(to: CGPoint(
                x: centre.x + normal.dx * half, y: centre.y + normal.dy * half))
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
