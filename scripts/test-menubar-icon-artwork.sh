#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/menubar_icon_contract.swift" <<'SWIFT'
import AppKit
import SwiftUI

func require(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() {
        fputs(message + "\n", stderr)
        exit(1)
    }
}

func visiblePixelCoverage(_ image: NSImage, scale: Int = 8) -> Double {
    let width = Int(image.size.width) * scale
    let height = Int(image.size.height) * scale
    let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil,
        pixelsWide: width,
        pixelsHigh: height,
        bitsPerSample: 8,
        samplesPerPixel: 4,
        hasAlpha: true,
        isPlanar: false,
        colorSpaceName: .deviceRGB,
        bytesPerRow: 0,
        bitsPerPixel: 0
    )!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    image.draw(in: NSRect(x: 0, y: 0, width: width, height: height))
    NSGraphicsContext.restoreGraphicsState()

    var visible = 0
    for y in 0..<height {
        for x in 0..<width where rep.colorAt(x: x, y: y)!.alphaComponent > 0.04 {
            visible += 1
        }
    }
    return Double(visible) / Double(width * height)
}

@main
struct MenuBarIconContract {
    static func main() {
        let steady = ClipfanMenuBarIconArtwork.steadyLabelImage()
        require(steady.isTemplate, "steady menu bar label image must be a template image")
        require(steady.size == NSSize(width: 22, height: 18), "steady menu bar label image has wrong size: \(steady.size)")
        let steadyCoverage = visiblePixelCoverage(steady)
        require(steadyCoverage > 0.08, "steady menu bar label image has too little visible artwork")
        require(steadyCoverage < 0.45, "steady menu bar label image has too much filled area")

        let card = ClipfanMenuBarIconArtwork.animatedCardImage()
        require(card.isTemplate, "animated menu bar card image must be a template image")
        require(card.size.width > 0 && card.size.height > 0, "animated menu bar card image must have non-zero size")
        let cardCoverage = visiblePixelCoverage(card)
        require(cardCoverage > 0.08, "animated menu bar card image has too little visible artwork")
        require(cardCoverage < 0.45, "animated menu bar card image has too much filled area")
    }
}
SWIFT

swiftc -parse-as-library \
  "$repo/apps/mac/Clipfan/Sources/Clipfan/MenuBarIcon.swift" \
  "$tmp/menubar_icon_contract.swift" \
  -o "$tmp/menubar_icon_contract"

"$tmp/menubar_icon_contract"
