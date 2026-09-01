import SwiftUI

// MARK: - Ice scene
//
// The brand world, drawn: a floe in calm water with a bear, three penguins, and
// a seal surfacing at its breathing hole.
//
// It earns its place by being the product's behaviour as a picture. A seal at an
// aglu waits under the ice on an irregular clock, rises to breathe, holds a
// moment, and sinks again, which is exactly what goguma does to a laptop. It
// is used only where there is genuinely nothing else to show: an empty job list,
// a daemon that is not running. Those are the moments a blank pane would waste,
// and the rule elsewhere in this app is that the interface stays quiet.
//
// Morning maps to light appearance, dusk to dark. The scene follows the Mac
// rather than carrying a control of its own.
//
// Motion is procedural rather than looped: each animal accelerates toward a
// target, eases to a stop, dawdles a random beat, and sometimes turns around, so
// the scene never repeats exactly. Under Reduce Motion nothing moves at all;
// the scene renders as a still, which is why every sprite is drawn from a pose
// that reads correctly at rest.

/// The scene, sized to fill whatever it is given.
struct IceScene: View {
    /// Canonical drawing space. Every coordinate below is in these units and is
    /// scaled to fit, so the artwork is resolution-independent and the numbers
    /// match the reference drawing one for one.
    private static let canvasSize = CGSize(width: 360, height: 250)

    /// Where the animals stand, measured up from the bottom.
    private static let surfaceLine: CGFloat = 96

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.colorScheme) private var colorScheme

    @State private var motion = IceSceneMotion()

    /// Whether anything of this scene can actually be seen.
    ///
    /// Starts false and is switched on by the first report, matching
    /// `WindowVisibilityReader`'s own convention of starting from "not
    /// visible". A scene that is about to be seen therefore holds its seeded
    /// still for the frame or two before the reader answers, which is a
    /// composed picture by design (see `IceSceneMotion.init`), rather than
    /// animating in a window that turns out to be hidden.
    @State private var onScreen = false

    var body: some View {
        GeometryReader { geo in
            let scale = min(
                geo.size.width / Self.canvasSize.width,
                geo.size.height / Self.canvasSize.height
            )
            let drawn = CGSize(
                width: Self.canvasSize.width * scale,
                height: Self.canvasSize.height * scale
            )
            let origin = CGPoint(
                x: (geo.size.width - drawn.width) / 2,
                y: (geo.size.height - drawn.height) / 2
            )

            Group {
                if reduceMotion {
                    // A single still frame. `advance` is never called, so the
                    // animals hold the resting pose they were seeded with.
                    canvas(scale: scale, origin: origin, frame: motion.frame)
                } else {
                    // Paused while nothing of the scene is on screen.
                    //
                    // A `TimelineView` runs for as long as its view exists, and
                    // a SwiftUI view exists whether or not anyone can see it.
                    // goguma builds its main window off screen half a second
                    // after launch so the first click on Jobs is instant
                    // (`WindowCoordinator.prewarm`), so the empty-list scene
                    // inside it animated from launch to quit having never once
                    // been visible.
                    //
                    // Paused rather than slowed: a paused schedule generates no
                    // further entries at all, so the cost is zero rather than
                    // lower. The last frame stays up, so what a window reveals
                    // when it appears is a composed scene, not a blank pane.
                    TimelineView(.animation(paused: !onScreen)) { timeline in
                        canvas(scale: scale, origin: origin,
                               frame: motion.advance(to: timeline.date))
                    }
                    // Attached here rather than to the `Group`, so a Reduce
                    // Motion still — which never reads `onScreen` — is not
                    // re-evaluated every time a window is covered or revealed.
                    .background(WindowVisibilityReader { onScreen = $0 })
                }
            }
        }
        .accessibilityElement()
        .accessibilityLabel(
            "An illustration of a polar bear and penguins on an ice floe, "
                + "with a seal surfacing at a breathing hole."
        )
    }

    private func canvas(scale: CGFloat, origin: CGPoint, frame: IceSceneFrame) -> some View {
        Canvas { context, _ in
            context.translateBy(x: origin.x, y: origin.y)
            context.scaleBy(x: scale, y: scale)
            draw(in: &context, frame)
        }
        // Drawing is pure geometry with no text or images, so it can run off
        // the main thread and never blocks the interface it decorates.
        .drawingGroup()
    }

    // MARK: - Composition

    private func draw(in context: inout GraphicsContext, _ frame: IceSceneFrame) {
        let h = Self.canvasSize.height
        drawWater(&context, h: h)
        drawFloe(&context, h: h)

        // Draw order is depth: penguins in front of the bear, the seal in front
        // of both, snow over everything.
        drawBear(&context, h: h, frame.bear)
        drawPenguins(&context, h: h, frame.penguins)
        drawSeal(&context, h: h, frame.seal)
        drawSnow(&context, frame.snow)
    }

    /// Three soft ellipses. Blur is what keeps the water from having an edge, so
    /// the scene melts into the pane behind it rather than sitting in a box.
    private func drawWater(_ context: inout GraphicsContext, h: CGFloat) {
        var layer = context
        layer.addFilter(.blur(radius: 12))
        layer.opacity = 0.85
        layer.fill(
            ellipse(cx: 184, cy: h - 86, rx: 172, ry: 74),
            with: .color(Theme.Colors.Ice.water)
        )

        var mid = context
        mid.addFilter(.blur(radius: 7))
        mid.opacity = 0.6
        mid.fill(
            ellipse(cx: 184, cy: h - 78, rx: 150, ry: 58),
            with: .color(Theme.Colors.Ice.water)
        )

        var deep = context
        deep.addFilter(.blur(radius: 7))
        deep.opacity = 0.18
        deep.fill(
            ellipse(cx: 186, cy: h - 70, rx: 138, ry: 40),
            with: .color(Theme.Colors.Ice.waterDeep)
        )
    }

    private func drawFloe(_ context: inout GraphicsContext, h: CGFloat) {
        let t = h - 100
        let floe = SVGPath.parse("""
            M58 \(t) C46 \(t - 16) 104 \(t - 26) 168 \(t - 22)
            C232 \(t - 18) 304 \(t - 26) 314 \(t)
            C326 \(t + 22) 300 \(t + 50) 248 \(t + 58)
            C188 \(t + 66) 104 \(t + 66) 62 \(t + 50)
            C30 \(t + 38) 44 \(t + 14) 58 \(t) Z
            """)

        context.fill(
            floe,
            with: .linearGradient(
                Gradient(colors: [Theme.Colors.Ice.floeTop, Theme.Colors.Ice.floeBottom]),
                startPoint: CGPoint(x: 186, y: t - 26),
                endPoint: CGPoint(x: 186, y: t + 58)
            )
        )

        // A blurred white stroke reads as light catching the rim of the ice.
        var rim = context
        rim.addFilter(.blur(radius: 7))
        rim.opacity = 0.55
        rim.stroke(floe, with: .color(Theme.Colors.Ice.snow), lineWidth: 6)

        context.opacity = 0.5
        context.fill(
            SVGPath.parse("""
                M70 \(t - 2) C120 \(t - 18) 236 \(t - 18) 300 \(t - 2)
                C300 \(t + 8) 236 \(t - 6) 168 \(t - 4)
                C112 \(t - 2) 78 \(t + 8) 70 \(t - 2) Z
                """),
            with: .color(Theme.Colors.Ice.snow)
        )
        context.opacity = 1

        var cracks = context
        cracks.opacity = 0.14
        for d in [
            "M150 \(h - 92) Q176 \(h - 66) 168 \(h - 44)",
            "M232 \(h - 90) Q240 \(h - 72) 262 \(h - 64)",
        ] {
            cracks.stroke(
                SVGPath.parse(d),
                with: .color(Theme.Colors.Ice.crack),
                style: StrokeStyle(lineWidth: 1, lineCap: .round)
            )
        }

        var specks = context
        specks.opacity = 0.6
        for (x, y, r) in [(120.0, h - 60, 2.4), (96.0, h - 44, 1.7), (300.0, h - 58, 2.2)] {
            specks.fill(
                ellipse(cx: x, cy: y, rx: r, ry: r),
                with: .color(Theme.Colors.Ice.snow)
            )
        }
    }

    // MARK: - Inhabitants

    private func drawBear(_ context: inout GraphicsContext, h: CGFloat, _ state: WalkerState) {
        let scale: CGFloat = 0.85
        var g = context
        g.translateBy(x: state.x, y: h - Self.surfaceLine)
        g.scaleBy(x: state.facing * scale, y: scale)
        // Sprite coordinates run downward from its own top-left, so lift it by
        // its full height to stand it on the surface line.
        g.translateBy(x: 0, y: -IceSprites.bearSize.height)

        // Ground shadow stays put while the body rocks above it.
        g.opacity = 0.18
        g.fill(ellipse(cx: 52, cy: 60, rx: 40, ry: 4.5), with: .color(Theme.Colors.Ice.crack))
        g.opacity = 1

        IceSprites.drawBear(
            in: &g,
            bodyLift: state.bodyLift,
            bodyTilt: state.bodyTilt,
            frontLeg: state.frontLeg,
            backLeg: state.backLeg
        )
    }

    private func drawPenguins(
        _ context: inout GraphicsContext, h: CGFloat, _ states: [WalkerState]
    ) {
        for state in states {
            var g = context
            g.translateBy(x: state.x, y: h - Self.surfaceLine + 2 - state.bottomOffset)
            g.scaleBy(x: state.facing * state.scale, y: state.scale)
            g.translateBy(x: 0, y: -IceSprites.penguinSize.height)

            g.opacity = 0.18
            g.fill(ellipse(cx: 20, cy: 50, rx: 11, ry: 3), with: .color(Theme.Colors.Ice.crack))
            g.opacity = 1

            IceSprites.drawPenguin(
                in: &g,
                bodyLift: state.bodyLift,
                bodyTilt: state.bodyTilt,
                leftFoot: state.leftFoot,
                rightFoot: state.rightFoot
            )
        }
    }

    /// The seal, and the hole it comes up through.
    ///
    /// The clip is the whole trick: a window whose bottom edge sits on the
    /// waterline, so the animal is genuinely hidden below the surface rather
    /// than faded out. It rises into view and sinks out of it.
    private func drawSeal(
        _ context: inout GraphicsContext, h: CGFloat, _ state: SurfacerState
    ) {
        let baseY = h - (Self.surfaceLine + 4)
        let holeCentre = CGPoint(x: 266, y: baseY)

        // The aglu: open water, dark at its centre.
        context.fill(
            ellipse(cx: holeCentre.x, cy: holeCentre.y, rx: 30, ry: 9),
            with: .color(Theme.Colors.Ice.floeTop)
        )
        context.fill(
            ellipse(cx: holeCentre.x, cy: holeCentre.y, rx: 25, ry: 7),
            with: .radialGradient(
                Gradient(colors: [
                    Theme.Colors.Ice.waterShallow, Theme.Colors.Ice.waterDeep,
                ]),
                center: CGPoint(x: holeCentre.x - 2, y: holeCentre.y - 2),
                startRadius: 0,
                endRadius: 26
            )
        )

        // Aligned on the seal's HEAD, not the centre of its sprite.
        //
        // The head sits at x≈69 in an 86-wide drawing, so centring the sprite
        // on the hole surfaced the animal at the hole's right-hand edge; it
        // read as a seal lying beside the opening rather than coming up
        // through it. Offsetting by the head puts the part that breathes over
        // the part that is open water.
        let headOffset: CGFloat = 69
        var clip = context
        clip.clip(to: Path(CGRect(x: holeCentre.x - 130, y: baseY - 168, width: 200, height: 168)))
        var g = clip
        g.translateBy(x: holeCentre.x - headOffset, y: baseY + state.y)
        g.rotate(by: .degrees(state.rotation))
        g.translateBy(x: 0, y: -IceSprites.sealSize.height)
        IceSprites.drawSeal(in: &g, headTurn: state.headTurn)

        // A ring on the water each time it breaks or leaves the surface.
        if state.rippleOpacity > 0.001 {
            let r = 22 * state.rippleScale
            var ring = context
            ring.opacity = state.rippleOpacity
            ring.stroke(
                ellipse(cx: holeCentre.x, cy: holeCentre.y, rx: r, ry: r * 0.3),
                with: .color(Theme.Colors.Ice.snow.opacity(0.8)),
                lineWidth: 1.5
            )
        }
    }

    private func drawSnow(_ context: inout GraphicsContext, _ flakes: [Flake]) {
        for flake in flakes {
            var g = context
            g.opacity = flake.opacity
            g.fill(
                ellipse(cx: flake.x, cy: flake.y, rx: flake.size, ry: flake.size),
                with: .color(Theme.Colors.Ice.snow)
            )
        }
    }

    private func ellipse(cx: CGFloat, cy: CGFloat, rx: CGFloat, ry: CGFloat) -> Path {
        Ellipse().path(in: CGRect(x: cx - rx, y: cy - ry, width: rx * 2, height: ry * 2))
    }
}

/// The scene as a floor for a list that has not filled its window.
///
/// A jobs list is usually short: a handful of entries in a window with a 320pt
/// minimum height, leaving a large blank rectangle under the last row. This
/// fills it, and only it, drawing nothing when the rows reach the bottom.
///
/// **It follows the Mac's appearance rather than being pinned to night.** Night
/// was tried first, on the reasoning that a sleeping machine is a dusk picture.
/// It is genuinely good in dark mode, where the moonlit floe glows against a
/// dark pane. On a light pane the same palette puts its dark water behind a
/// pale surface and the whole scene reads as a smudge under the list. The
/// scene already adapts, and the version drawn for each appearance is the one
/// that belongs there.
struct IceSceneFooter: View {
    let rowCount: Int

    /// Below this there is not enough room to read the scene, and a squashed
    /// floe with clipped animals is worse than an honest empty space.
    private static let minimumUsefulHeight: CGFloat = 96

    /// Full height. Past this the scene stops growing and sits on the floor
    /// rather than ballooning to fill a tall window.
    private static let maximumHeight: CGFloat = 230

    /// What one row costs, for estimating where the rows stop.
    ///
    /// Approximate on purpose. The exact figure comes from AppKit's own row
    /// metrics, which are not readable from here, and being a few points out
    /// only shifts a decorative horizon; being wrong in the safe direction
    /// simply means the scene appears one row later than it could.
    private static let estimatedRowHeight: CGFloat = 30

    /// How far up to nudge the scene, as a fraction of its height.
    ///
    /// The drawing is not centred in its own canvas: the floe sits across the
    /// lower half and the sky above it is empty, so a frame centred on the gap
    /// still reads as bottom-heavy. This lifts the visible mass rather than the
    /// box that contains it.
    private static let contentBias: CGFloat = 0.10

    var body: some View {
        GeometryReader { geo in
            let used = CGFloat(rowCount) * Self.estimatedRowHeight
            let gap = geo.size.height - used
            let height = min(gap, Self.maximumHeight)
            if height >= Self.minimumUsefulHeight {
                // Centred in the gap, not pinned to the floor of the list.
                //
                // Pinning was wrong whenever the gap exceeded `maximumHeight`:
                // the scene stopped growing, stayed at the bottom edge, and
                // left every extra point of a tall window stacked above it, so
                // a taller window made the composition look worse rather than
                // roomier.
                IceScene()
                    .frame(height: height)
                    .frame(maxWidth: .infinity)
                    // Decoration behind a list of controls: never a tap target,
                    // and never announced.
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)
                    .position(
                        x: geo.size.width / 2,
                        y: used + gap / 2 - height * Self.contentBias
                    )
            }
        }
    }
}
