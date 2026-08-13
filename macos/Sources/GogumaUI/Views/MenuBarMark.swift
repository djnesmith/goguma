import AppKit

/// The snowscroll bear: the product's mark, drawn for the menu bar.
///
/// Geometry is the supplied artwork verbatim, normalised from its 120×120
/// viewBox. A solid silhouette of three circles with the eyes and nose punched
/// out, which is what an earlier attempt of mine was missing. Without the
/// punched features a bear built from circles reads as a generic round-eared
/// animal; the two holes and the muzzle are the whole identity.
///
/// **The constraint that shapes everything here.** A menu bar image is a
/// template: macOS strips the colour and tints the alpha itself so the glyph
/// matches a light bar, a dark bar, and the highlighted state. Nothing here can
/// mean anything by colour. State has to read from silhouette alone at 16pt.
///
/// So the bear carries the two everyday states and differs by **whether its
/// eyes are open**: awake while holding sleep off, asleep while idle. That is
/// the mark's own idea rather than one imposed on it, and it is a genuine
/// silhouette change that survives greyscale.
///
/// The three exceptional states keep standard SF Symbols. A cutout, a pause and
/// a dead daemon are not moods of the bear, they are conditions the user has to
/// notice and act on, and a familiar warning triangle says so faster than any
/// house mark could. Brand loyalty is the wrong instinct when something is
/// broken.
enum MenuBarMark {

    /// The supplied artwork, in its own 120×120 units. Kept unscaled so it can
    /// be diffed against the source SVG rather than trusted.
    private enum Art {
        static let canvas: CGFloat = 120

        static let earRadius: CGFloat = 16
        static let earLeft = CGPoint(x: 34, y: 33)
        static let earRight = CGPoint(x: 86, y: 33)

        static let headCentre = CGPoint(x: 60, y: 65)
        static let headRadius: CGFloat = 40

        static let eyeRadius: CGFloat = 4.4
        static let eyeLeft = CGPoint(x: 46, y: 59)
        static let eyeRight = CGPoint(x: 74, y: 59)

        static let noseCentre = CGPoint(x: 60, y: 78)
        static let noseRadius = CGSize(width: 6.8, height: 5.4)

        /// A closed eye is the open eye's width, flattened and rounded.
        ///
        /// Drawn rather than simply omitted: an eyeless bear reads as a bear
        /// facing away, not a sleeping one. Keeping the width means the closed
        /// state occupies the same visual slot, so the two glyphs differ by the
        /// shape of the hole rather than by whether there is one.
        static let closedEyeSize = CGSize(width: 11, height: 2.6)

        /// The bounding box of the drawn bear inside the 120×120 viewBox.
        ///
        /// The artwork is inset: ears start at y=17 and the head bottom is at
        /// y=105, leaving roughly 15% dead space on every side. Drawing the
        /// viewBox straight into a 16pt slot therefore produced a 13pt bear
        /// with a transparent border, which looked small next to neighbouring
        /// menu bar icons that fill their own space. Cropping to the ink lets
        /// the mark use the whole glyph.
        static var inkBounds: CGRect {
            let minX = earLeft.x - earRadius
            let maxX = earRight.x + earRadius
            let minY = earLeft.y - earRadius
            let maxY = headCentre.y + headRadius
            return CGRect(x: minX, y: minY, width: maxX - minX, height: maxY - minY)
        }
    }

    /// Builds the mark at `size` points.
    ///
    /// - Parameter colour: the ink. `nil` produces a **template** image, which
    ///   macOS tints itself, correct in a popover or a menu, where the system
    ///   colour is what you want.
    ///
    ///   Pass a colour for the menu bar. `NSStatusBarButton` **ignores
    ///   `contentTintColor` on a template image**: the menu bar draws templates
    ///   in its own text colour and there is no way to talk it out of that, so
    ///   the mark came out black no matter what tint was set on the button. The
    ///   colour has to be in the image. That costs the automatic inversion a
    ///   template gets when the item is highlighted, which is why the caller
    ///   resolves the colour against the button's own appearance and redraws
    ///   when it changes.
    static func image(size: CGFloat, asleep: Bool, colour: NSColor? = nil) -> NSImage {
        let image = NSImage(size: NSSize(width: size, height: size), flipped: true) { rect in
            guard let context = NSGraphicsContext.current?.cgContext else { return false }
            context.saveGState()
            // Fit the ink, not the viewBox. Uniform scale so the bear keeps its
            // proportions, then centred in whatever slot is left over.
            let ink = Art.inkBounds
            let scale = min(rect.width / ink.width, rect.height / ink.height)
            context.translateBy(
                x: (rect.width - ink.width * scale) / 2,
                y: (rect.height - ink.height * scale) / 2
            )
            context.scaleBy(x: scale, y: scale)
            context.translateBy(x: -ink.minX, y: -ink.minY)

            // A template's colour is ignored, so black is simply the alpha
            // carrier there; for the menu bar this is the real ink.
            context.setFillColor((colour ?? .black).cgColor)
            context.addPath(silhouette())
            context.fillPath()

            // The source SVG punches the features with a mask; clearing them
            // here gives the same result and keeps the image a single alpha
            // channel, which is what makes a template valid.
            context.setBlendMode(.clear)
            context.addPath(features(asleep: asleep))
            context.fillPath()

            context.restoreGState()
            return true
        }
        image.isTemplate = colour == nil
        return image
    }

    /// Ears and head. Overlapping circles filled non-zero, so they union.
    private static func silhouette() -> CGPath {
        let path = CGMutablePath()
        path.addEllipse(in: square(Art.earLeft, Art.earRadius))
        path.addEllipse(in: square(Art.earRight, Art.earRadius))
        path.addEllipse(in: square(Art.headCentre, Art.headRadius))
        return path
    }

    /// The holes: two eyes and a muzzle.
    private static func features(asleep: Bool) -> CGPath {
        let path = CGMutablePath()
        if asleep {
            for centre in [Art.eyeLeft, Art.eyeRight] {
                let rect = CGRect(
                    x: centre.x - Art.closedEyeSize.width / 2,
                    y: centre.y - Art.closedEyeSize.height / 2,
                    width: Art.closedEyeSize.width,
                    height: Art.closedEyeSize.height
                )
                path.addRoundedRect(
                    in: rect,
                    cornerWidth: Art.closedEyeSize.height / 2,
                    cornerHeight: Art.closedEyeSize.height / 2
                )
            }
        } else {
            path.addEllipse(in: square(Art.eyeLeft, Art.eyeRadius))
            path.addEllipse(in: square(Art.eyeRight, Art.eyeRadius))
        }
        path.addEllipse(
            in: CGRect(
                x: Art.noseCentre.x - Art.noseRadius.width,
                y: Art.noseCentre.y - Art.noseRadius.height,
                width: Art.noseRadius.width * 2,
                height: Art.noseRadius.height * 2
            )
        )
        return path
    }

    private static func square(_ centre: CGPoint, _ radius: CGFloat) -> CGRect {
        CGRect(
            x: centre.x - radius, y: centre.y - radius,
            width: radius * 2, height: radius * 2
        )
    }
}
