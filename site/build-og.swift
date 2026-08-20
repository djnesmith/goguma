import AppKit

// Renders the 1200x630 card that Reddit, Slack, iMessage and X show when
// somebody pastes a getgoguma.com link.
//
//     swift build-og.swift assets/og.png
//
// Built rather than cropped from a screenshot. The obvious shortcut — take
// `scene-close.webp` and cut it to 1.91:1 — slices the menu bar's words in half
// at both edges ("nder", "Fr") and leaves a third of the frame as empty desktop,
// because that picture was composed for a different aspect. A card has to be
// composed for the box it is shown in.
//
// PNG rather than the WebP the page uses: several unfurlers still will not
// decode WebP, and the one image whose whole job is to appear elsewhere is the
// wrong place to be ahead of the field.
let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "assets/og.png"

let W = 1200.0, H = 630.0
// The site's own tokens, from index.html :root.
let ground = NSColor(srgbRed: 0.937, green: 0.894, blue: 0.941, alpha: 1) // #EFE4F0
let ink = NSColor(srgbRed: 0.141, green: 0.102, blue: 0.173, alpha: 1)    // #241A2C
let ink2 = NSColor(srgbRed: 0.420, green: 0.376, blue: 0.447, alpha: 1)   // #6B6072

guard let rep = NSBitmapImageRep(
    bitmapDataPlanes: nil, pixelsWide: Int(W), pixelsHigh: Int(H),
    bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
    colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)
else { exit(1) }

NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
let ctx = NSGraphicsContext.current!.cgContext

ground.setFill()
NSRect(x: 0, y: 0, width: W, height: H).fill()

// The plate, whole, on the right. `plate-idle.webp` is the popover already on
// paper with nothing else in frame, which is why it is the asset used here
// rather than the desktop scene.
let plateW = 560.0
if let plate = NSImage(contentsOfFile: "assets/plate-idle.webp") {
    let scale = plateW / plate.size.width
    let plateH = plate.size.height * scale
    let box = NSRect(x: W - plateW - 56, y: (H - plateH) / 2, width: plateW, height: plateH)
    // The contact shadow the page gives every panel, so it sits on the ground
    // rather than floating in front of it.
    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -14), blur: 44,
                  color: NSColor(srgbRed: 0.141, green: 0.102, blue: 0.173, alpha: 0.22).cgColor)
    let clip = NSBezierPath(roundedRect: box, xRadius: 18, yRadius: 18)
    clip.addClip()
    plate.draw(in: box)
    ctx.restoreGState()
}

// The wordmark and the line the site leads with, left, on the page's scale:
// weight 600, tracking -.035em, leading 1.04.
let left = 72.0
let colW = W - plateW - 56 - left - 44

func draw(_ s: String, _ size: CGFloat, _ colour: NSColor, _ tracking: CGFloat,
          _ y: CGFloat, weight: NSFont.Weight = .semibold, mono: Bool = false) -> CGFloat {
    let font = mono
        ? NSFont.monospacedSystemFont(ofSize: size, weight: weight)
        : NSFont.systemFont(ofSize: size, weight: weight)
    let para = NSMutableParagraphStyle()
    para.lineHeightMultiple = 1.04
    let a = NSAttributedString(string: s, attributes: [
        .font: font, .foregroundColor: colour,
        .kern: size * tracking, .paragraphStyle: para,
    ])
    let box = a.boundingRect(with: NSSize(width: colW, height: 400),
                             options: [.usesLineFragmentOrigin])
    a.draw(with: NSRect(x: left, y: y - box.height, width: colW, height: box.height),
           options: [.usesLineFragmentOrigin])
    return box.height
}

var y = H - 150
y -= draw("goguma", 62, ink, -0.035, y) + 26
y -= draw("Close the lid.\nThe jobs still run.", 52, ink, -0.035, y) + 30
_ = draw("free · open source · macOS 14+", 20, ink2, 0.02, y, weight: .regular, mono: true)

NSGraphicsContext.restoreGraphicsState()
try? rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: out))
