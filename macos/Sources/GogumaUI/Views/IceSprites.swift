import SwiftUI

// MARK: - SVG path data

/// A small parser for the subset of SVG path syntax the artwork uses.
///
/// The alternative was hand-translating some thirty bezier commands into
/// `Path` builder calls, which is mechanical, unreviewable, and wrong the
/// moment a single coordinate is mistyped. Keeping the original `d` strings
/// verbatim means the drawing here is provably the drawing that was designed,
/// and a change to the artwork is a copy-paste rather than a re-translation.
///
/// Supports M/m, L/l, H/h, V/v, C/c, Q/q, Z/z; everything the sprites use.
/// Anything else is ignored rather than throwing: a missing flourish is a far
/// better failure than an empty pane.
enum SVGPath {
    static func parse(_ d: String) -> Path {
        var path = Path()
        var current = CGPoint.zero
        var start = CGPoint.zero
        var command: Character = "M"
        var numbers: [CGFloat] = []
        var index = d.startIndex

        func flush() {
            guard !numbers.isEmpty || command == "Z" || command == "z" else { return }
            var i = 0
            let relative = command.isLowercase

            func point(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
                relative ? CGPoint(x: current.x + x, y: current.y + y) : CGPoint(x: x, y: y)
            }

            switch command {
            case "M", "m":
                while i + 1 < numbers.count || i + 2 <= numbers.count {
                    guard i + 2 <= numbers.count else { break }
                    let p = point(numbers[i], numbers[i + 1])
                    if i == 0 {
                        path.move(to: p)
                        start = p
                    } else {
                        path.addLine(to: p)
                    }
                    current = p
                    i += 2
                }
            case "L", "l":
                while i + 2 <= numbers.count {
                    let p = point(numbers[i], numbers[i + 1])
                    path.addLine(to: p)
                    current = p
                    i += 2
                }
            case "H", "h":
                while i < numbers.count {
                    let p = CGPoint(x: relative ? current.x + numbers[i] : numbers[i], y: current.y)
                    path.addLine(to: p)
                    current = p
                    i += 1
                }
            case "V", "v":
                while i < numbers.count {
                    let p = CGPoint(x: current.x, y: relative ? current.y + numbers[i] : numbers[i])
                    path.addLine(to: p)
                    current = p
                    i += 1
                }
            case "C", "c":
                while i + 6 <= numbers.count {
                    let c1 = point(numbers[i], numbers[i + 1])
                    let c2 = point(numbers[i + 2], numbers[i + 3])
                    let p = point(numbers[i + 4], numbers[i + 5])
                    path.addCurve(to: p, control1: c1, control2: c2)
                    current = p
                    i += 6
                }
            case "Q", "q":
                while i + 4 <= numbers.count {
                    let c = point(numbers[i], numbers[i + 1])
                    let p = point(numbers[i + 2], numbers[i + 3])
                    path.addQuadCurve(to: p, control: c)
                    current = p
                    i += 4
                }
            case "Z", "z":
                path.closeSubpath()
                current = start
            default:
                break
            }
            numbers.removeAll(keepingCapacity: true)
        }

        while index < d.endIndex {
            let ch = d[index]
            if ch.isLetter {
                flush()
                command = ch
                index = d.index(after: index)
                if ch == "Z" || ch == "z" { flush() }
                continue
            }
            if ch.isNumber || ch == "-" || ch == "." || ch == "+" {
                var text = String(ch)
                index = d.index(after: index)
                while index < d.endIndex {
                    let c = d[index]
                    // A '-' starts a new number unless it follows an exponent,
                    // and a second '.' likewise: "1.5.5" is two numbers in SVG.
                    if c.isNumber {
                        text.append(c)
                    } else if c == "." && !text.contains(".") {
                        text.append(c)
                    } else if (c == "e" || c == "E") && !text.lowercased().contains("e") {
                        text.append(c)
                    } else if (c == "-" || c == "+") && (text.last == "e" || text.last == "E") {
                        text.append(c)
                    } else {
                        break
                    }
                    index = d.index(after: index)
                }
                if let value = Double(text) { numbers.append(CGFloat(value)) }
                continue
            }
            index = d.index(after: index)
        }
        flush()
        return path
    }
}

// MARK: - Sprites

/// The three animals, drawn from the reference artwork's own path data.
///
/// Each is split into the groups that need to move independently (legs, a
/// head, a body that rocks over planted feet) because a sprite drawn as one
/// shape can only slide, and sliding is what makes an animated illustration
/// look cheap.
enum IceSprites {
    static let bearSize = CGSize(width: 112, height: 64)
    static let penguinSize = CGSize(width: 40, height: 54)
    static let sealSize = CGSize(width: 86, height: 46)

    // MARK: Bear

    static func drawBear(
        in context: inout GraphicsContext,
        bodyLift: CGFloat,
        bodyTilt: Double,
        frontLeg: Double,
        backLeg: Double
    ) {
        var body = context
        // The body pivots near its base, so a tilt rocks it rather than
        // swinging it sideways off the legs.
        body.translateBy(x: 56, y: 63)
        body.rotate(by: .degrees(bodyTilt))
        body.translateBy(x: -56, y: -63 + bodyLift)

        drawLeg(in: &body, x: 17, angle: backLeg)
        drawLeg(in: &body, x: 64, angle: frontLeg)

        let fill = Theme.Colors.Ice.bear
        let line = Theme.Colors.Ice.bearLine

        let torso = SVGPath.parse("""
            M16 34 C12 22 22 14 40 14 C58 14 74 14 82 18
            C86 20 88 24 88 28 C92 30 96 33 95 40
            C93 44 86 42 82 40 C70 36 30 36 22 40 C17 42 14 40 16 34 Z
            """)
        body.fill(torso, with: .color(fill))
        body.stroke(torso, with: .color(line), lineWidth: 1.4)

        let haunch = SVGPath.parse("M82 19 C90 17 99 19 103 26 C106 31 104 38 98 39 C92 40 86 36 84 30 Z")
        body.fill(haunch, with: .color(fill))
        body.stroke(haunch, with: .color(line), lineWidth: 1.4)

        let ear = circle(90, 19, 4.6)
        body.fill(ear, with: .color(fill))
        body.stroke(ear, with: .color(line), lineWidth: 1.3)

        let muzzle = oval(104, 33, 6.2, 4.8)
        body.fill(muzzle, with: .color(fill))
        body.stroke(muzzle, with: .color(line), lineWidth: 1.2)

        body.fill(circle(95, 27, 1.7), with: .color(Theme.Colors.Ice.bearEye))
        body.fill(oval(109, 33, 2.2, 1.8), with: .color(Theme.Colors.Ice.bearEye))
    }

    private static func drawLeg(in context: inout GraphicsContext, x: CGFloat, angle: Double) {
        var leg = context
        // Legs swing from the hip, which is the top of the shape.
        leg.translateBy(x: x + 4.75, y: 34.5)
        leg.rotate(by: .degrees(angle))
        leg.translateBy(x: -(x + 4.75), y: -34.5)

        leg.fill(
            rounded(x: x, y: 33, w: 9.5, h: 24, r: 4.7),
            with: .color(Theme.Colors.Ice.bearShade)
        )
        let front = rounded(x: x + 10, y: 34, w: 9.5, h: 23, r: 4.7)
        leg.fill(front, with: .color(Theme.Colors.Ice.bear))
        leg.stroke(front, with: .color(Theme.Colors.Ice.bearLine), lineWidth: 1)
    }

    // MARK: Penguin

    static func drawPenguin(
        in context: inout GraphicsContext,
        bodyLift: CGFloat,
        bodyTilt: Double,
        leftFoot: Double,
        rightFoot: Double
    ) {
        drawFoot(in: &context, d: "M14 45 q-4 1 -5 4 q3 1 6 -1 Z", pivot: CGPoint(x: 14, y: 45), angle: leftFoot)
        drawFoot(in: &context, d: "M26 45 q4 1 5 4 q-3 1 -6 -1 Z", pivot: CGPoint(x: 26, y: 45), angle: rightFoot)

        var body = context
        body.translateBy(x: 20, y: 48)
        body.rotate(by: .degrees(bodyTilt))
        body.translateBy(x: -20, y: -48 + bodyLift)

        body.fill(
            SVGPath.parse("M20 4 C28 4 31 12 31 24 C31 38 27 47 20 47 C13 47 9 38 9 24 C9 12 12 4 20 4 Z"),
            with: .color(Theme.Colors.Ice.penguinBack)
        )
        body.fill(
            SVGPath.parse("M20 13 C25 13 27 19 27 27 C27 37 24 44 20 44 C16 44 13 37 13 27 C13 19 15 13 20 13 Z"),
            with: .color(Theme.Colors.Ice.penguinFront)
        )
        body.fill(
            SVGPath.parse("M10 18 C6 22 6 30 9 34 C10 30 11 24 12 20 Z"),
            with: .color(Theme.Colors.Ice.penguinBack)
        )
        body.fill(circle(16.5, 16, 2.1), with: .color(Theme.Colors.Ice.penguinFront))
        body.fill(circle(16.7, 16.2, 1), with: .color(Theme.Colors.Ice.sealEye))
        body.fill(circle(23.5, 16, 2.1), with: .color(Theme.Colors.Ice.penguinFront))
        body.fill(circle(23.3, 16.2, 1), with: .color(Theme.Colors.Ice.sealEye))
        body.fill(SVGPath.parse("M24 19.5 L30 21 L24 22.6 Z"), with: .color(Theme.Colors.Ice.beak))
    }

    private static func drawFoot(
        in context: inout GraphicsContext, d: String, pivot: CGPoint, angle: Double
    ) {
        var foot = context
        foot.translateBy(x: pivot.x, y: pivot.y)
        foot.rotate(by: .degrees(angle))
        foot.translateBy(x: -pivot.x, y: -pivot.y)
        foot.fill(SVGPath.parse(d), with: .color(Theme.Colors.Ice.beak))
    }

    // MARK: Seal

    static func drawSeal(in context: inout GraphicsContext, headTurn: Double) {
        context.fill(
            SVGPath.parse("M14 30 C5 27 3 33 8 33 C2 37 11 38 16 33 Z"),
            with: .color(Theme.Colors.Ice.sealDark)
        )
        context.fill(
            SVGPath.parse("M15 31 C9 30 8 34 12 34 C8 36 13 37 17 33 Z"),
            with: .color(Theme.Colors.Ice.sealLight)
        )
        context.fill(
            SVGPath.parse("""
                M14 31 C12 22 26 19 40 20 C54 21 60 18 66 18 C61 24 62 27 66 29
                C60 33 44 33 32 33 C24 33 17 34 14 31 Z
                """),
            with: .color(Theme.Colors.Ice.sealBody)
        )
        context.fill(
            SVGPath.parse("M22 30 C26 25 40 25 50 26 C56 26.5 58 28 56 29 C46 28.6 34 28.6 28 30 C24.5 30.6 21 31 22 30 Z"),
            with: .color(Theme.Colors.Ice.sealLight)
        )
        context.fill(
            SVGPath.parse("M38 31 C40 36 45 38 48 36 C47 33 44 31 42 30 Z"),
            with: .color(Theme.Colors.Ice.sealDark)
        )

        var head = context
        head.translateBy(x: 60, y: 25)
        head.rotate(by: .degrees(headTurn))
        head.translateBy(x: -60, y: -25)

        head.fill(
            SVGPath.parse("M58 22 C56 15 62 11 69 12 C77 13 80 19 76 24 C72 28 64 28 60 25 Z"),
            with: .color(Theme.Colors.Ice.sealBody)
        )
        head.fill(
            SVGPath.parse("M62 21 C61 16 66 14 70 15 C74 16 75 20 72 23 C69 25 64 24 62 21 Z"),
            with: .color(Theme.Colors.Ice.sealLight)
        )
        head.fill(circle(69, 18.4, 1.7), with: .color(Theme.Colors.Ice.sealEye))
        head.opacity = 0.85
        head.fill(circle(69.6, 17.8, 0.5), with: .color(Theme.Colors.Ice.snow))
        head.opacity = 1
        head.fill(circle(77.5, 20.2, 1.5), with: .color(Theme.Colors.Ice.sealEye))
        for d in ["M76 22 q4 0.6 6 -0.2", "M76 23.4 q4 0.8 6 0.2"] {
            head.stroke(
                SVGPath.parse(d),
                with: .color(Theme.Colors.Ice.sealDark),
                style: StrokeStyle(lineWidth: 0.7, lineCap: .round)
            )
        }
    }

    // MARK: Shape helpers

    private static func circle(_ cx: CGFloat, _ cy: CGFloat, _ r: CGFloat) -> Path {
        oval(cx, cy, r, r)
    }

    private static func oval(_ cx: CGFloat, _ cy: CGFloat, _ rx: CGFloat, _ ry: CGFloat) -> Path {
        Ellipse().path(in: CGRect(x: cx - rx, y: cy - ry, width: rx * 2, height: ry * 2))
    }

    private static func rounded(
        x: CGFloat, y: CGFloat, w: CGFloat, h: CGFloat, r: CGFloat
    ) -> Path {
        Path(roundedRect: CGRect(x: x, y: y, width: w, height: h), cornerRadius: r)
    }
}
