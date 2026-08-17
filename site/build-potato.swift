import AppKit

// Extracts Apple's 🍠 from the system emoji font, cropped to its ink — the same
// thing `SweetPotatoMark.artwork()` does inside the app, so the page's favicon
// and brand mark are literally the glyph the menu bar shows.
//
// Rendered once at a large point size and cropped rather than drawn at the size
// wanted: Apple Color Emoji is a bitmap font with fixed strikes, so its metrics
// are not proportional and ink measured at one size does not predict ink at
// another.
let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "assets/potato.png"
let text = NSAttributedString(string: "\u{1F360}", attributes: [.font: NSFont.systemFont(ofSize: 256)])
let box = text.size()
let canvas = NSSize(width: box.width * 2, height: box.height * 2)
guard let rep = NSBitmapImageRep(
    bitmapDataPlanes: nil, pixelsWide: Int(canvas.width.rounded(.up)),
    pixelsHigh: Int(canvas.height.rounded(.up)), bitsPerSample: 8, samplesPerPixel: 4,
    hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)
else { exit(1) }
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
text.draw(at: NSPoint(x: box.width / 2, y: box.height / 2))
NSGraphicsContext.restoreGraphicsState()

var minX = rep.pixelsWide, maxX = -1, minY = rep.pixelsHigh, maxY = -1
for y in 0..<rep.pixelsHigh {
    for x in 0..<rep.pixelsWide where (rep.colorAt(x: x, y: y)?.alphaComponent ?? 0) > 0.02 {
        minX = min(minX, x); maxX = max(maxX, x); minY = min(minY, y); maxY = max(maxY, y)
    }
}
guard maxX >= minX, let full = rep.cgImage,
      let art = full.cropping(to: CGRect(x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1))
else { exit(1) }
let outRep = NSBitmapImageRep(cgImage: art)
try? outRep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: out))
print("wrote \(out) \(art.width)x\(art.height)")
