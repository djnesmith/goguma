import AppKit

// Renders the menu bar's SF Symbols to PNG so `build-scenes.py` can composite
// Apple's own glyphs. Drawing wifi and battery by hand was tried and is always
// subtly wrong, which is the kind of wrong a Mac user notices instantly.
let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "symbols"
let specs: [(String, String)] = [
    ("wifi", "wifi"), ("battery.75percent", "battery"), ("switch.2", "control"),
    ("magnifyingglass", "search"), ("apple.logo", "apple"),
]
for (name, file) in specs {
    let cfg = NSImage.SymbolConfiguration(pointSize: 64, weight: .regular)
    guard let img = NSImage(systemSymbolName: name, accessibilityDescription: nil)?
        .withSymbolConfiguration(cfg) else {
        FileHandle.standardError.write(Data("missing \(name)\n".utf8)); continue
    }
    let size = img.size
    guard let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: Int(size.width.rounded(.up)),
        pixelsHigh: Int(size.height.rounded(.up)), bitsPerSample: 8, samplesPerPixel: 4,
        hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
        bytesPerRow: 0, bitsPerPixel: 0) else { continue }
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    img.draw(in: NSRect(origin: .zero, size: size))
    NSColor.white.set()
    NSRect(origin: .zero, size: size).fill(using: .sourceAtop)
    NSGraphicsContext.restoreGraphicsState()
    try? rep.representation(using: .png, properties: [:])!
        .write(to: URL(fileURLWithPath: "\(out)/\(file).png"))
    print("\(file) \(Int(size.width))x\(Int(size.height))")
}
