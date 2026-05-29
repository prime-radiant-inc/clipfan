#!/usr/bin/env bash
# Renders the clipfan app icon (graphite cards fanned out, one accent card) at all
# required sizes and packs them into Resources/AppIcon.icns via iconutil.
#
# Must run in a logged-in GUI session: the master is drawn with NSImage.lockFocus,
# which needs a window-server connection (it can fail on headless CI runners). The
# generated .icns is committed, so normal builds never regenerate it.
set -euo pipefail
cd "$(dirname "$0")"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
iconset="$work/AppIcon.iconset"
mkdir -p "$iconset" Resources

# Draw a 1024px master with Swift + CoreGraphics, then downscale with sips.
cat > "$work/draw.swift" <<'SWIFT'
import AppKit

let size = 1024
let img = NSImage(size: NSSize(width: size, height: size))
img.lockFocus()
let ctx = NSGraphicsContext.current!.cgContext

let rect = CGRect(x: 0, y: 0, width: size, height: size)
let bgPath = CGPath(roundedRect: rect.insetBy(dx: 80, dy: 80),
                    cornerWidth: 180, cornerHeight: 180, transform: nil)
ctx.addPath(bgPath)
let colors = [NSColor(calibratedWhite: 0.22, alpha: 1).cgColor,
              NSColor(calibratedWhite: 0.12, alpha: 1).cgColor] as CFArray
let grad = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(), colors: colors, locations: [0, 1])!
ctx.saveGState(); ctx.clip()
ctx.drawLinearGradient(grad, start: CGPoint(x: 0, y: size), end: CGPoint(x: size, y: 0), options: [])
ctx.restoreGState()

func card(cx: CGFloat, cy: CGFloat, angle: CGFloat, fill: NSColor) {
    ctx.saveGState()
    ctx.translateBy(x: cx, y: cy)
    ctx.rotate(by: angle * .pi / 180)
    let w: CGFloat = 300, h: CGFloat = 380
    let r = CGRect(x: -w/2, y: -h/2, width: w, height: h)
    let p = CGPath(roundedRect: r, cornerWidth: 40, cornerHeight: 40, transform: nil)
    ctx.addPath(p); ctx.setFillColor(fill.cgColor); ctx.fillPath()
    ctx.restoreGState()
}
card(cx: 430, cy: 520, angle: 14, fill: NSColor(calibratedWhite: 0.80, alpha: 1))
card(cx: 512, cy: 512, angle: 0,  fill: NSColor(calibratedWhite: 0.92, alpha: 1))
card(cx: 594, cy: 504, angle: -14, fill: NSColor.systemBlue)

img.unlockFocus()
let tiff = img.tiffRepresentation!
let rep = NSBitmapImageRep(data: tiff)!
let png = rep.representation(using: .png, properties: [:])!
try! png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
SWIFT

master="$work/master.png"
swift "$work/draw.swift" "$master"

gen() { sips -z "$2" "$2" "$master" --out "$iconset/icon_$1.png" >/dev/null; }
gen 16x16        16
gen 16x16@2x     32
gen 32x32        32
gen 32x32@2x     64
gen 128x128      128
gen 128x128@2x   256
gen 256x256      256
gen 256x256@2x   512
gen 512x512      512
gen 512x512@2x   1024

iconutil -c icns "$iconset" -o Resources/AppIcon.icns
echo "Wrote Resources/AppIcon.icns"
