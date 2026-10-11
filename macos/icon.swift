// Draws the app icon into an .iconset folder: swift icon.swift <folder>
import AppKit

func draw(pixels: Int) -> Data {
    let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8,
        samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
        bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    // everything below is laid out on a 1024-point canvas
    let scale = CGFloat(pixels) / 1024
    let transform = NSAffineTransform()
    transform.scale(by: scale)
    transform.concat()

    let tile = NSBezierPath(roundedRect: NSRect(x: 100, y: 100, width: 824, height: 824), xRadius: 185, yRadius: 185)
    NSGradient(
        starting: NSColor(srgbRed: 0.35, green: 0.56, blue: 1, alpha: 1),
        ending: NSColor(srgbRed: 0.17, green: 0.36, blue: 0.87, alpha: 1))!
        .draw(in: tile, angle: -90)

    // one line that splits into two
    NSColor.white.set()
    let stem = NSPoint(x: 300, y: 512), fork = NSPoint(x: 490, y: 512)
    let up = NSPoint(x: 724, y: 692), down = NSPoint(x: 724, y: 332)
    let lines = NSBezierPath()
    lines.lineWidth = 56
    lines.lineCapStyle = .round
    lines.lineJoinStyle = .round
    lines.move(to: stem)
    lines.line(to: fork)
    lines.line(to: up)
    lines.move(to: fork)
    lines.line(to: down)
    lines.stroke()
    for dot in [stem, up, down] {
        NSBezierPath(ovalIn: NSRect(x: dot.x - 62, y: dot.y - 62, width: 124, height: 124)).fill()
    }

    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

let folder = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
for points in [16, 32, 128, 256, 512] {
    try draw(pixels: points).write(to: folder.appendingPathComponent("icon_\(points)x\(points).png"))
    try draw(pixels: points * 2).write(to: folder.appendingPathComponent("icon_\(points)x\(points)@2x.png"))
}
