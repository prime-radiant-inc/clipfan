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

func meanAlpha(_ image: NSImage, scale: Int = 8) -> Double {
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

    var alpha = 0.0
    for y in 0..<height {
        for x in 0..<width {
            alpha += rep.colorAt(x: x, y: y)!.alphaComponent
        }
    }
    return alpha / Double(width * height)
}

func alphaAt(_ image: NSImage, x: CGFloat, y: CGFloat, scale: Int = 8) -> Double {
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

    return rep.colorAt(x: Int(x * CGFloat(scale)), y: Int(y * CGFloat(scale)))!.alphaComponent
}

func requireClose(_ actual: CGFloat, _ expected: CGFloat, accuracy: CGFloat, _ message: String) {
    require(abs(actual - expected) <= accuracy, "\(message): expected \(expected), got \(actual)")
}

func frame(_ frames: [MenuBarFanCardFrame], id: MenuBarFanCardFrame.ID) -> MenuBarFanCardFrame {
    guard let frame = frames.first(where: { $0.id == id }) else {
        fputs("missing animation frame for \(id)\n", stderr)
        exit(1)
    }
    return frame
}

struct PixelToneStats {
    let bright: Int
    let dark: Int
    let visible: Int
}

@MainActor
func renderedToneStats(colorScheme: ColorScheme) -> PixelToneStats {
    let view = NSHostingView(
        rootView: ClipfanMenuBarIcon(isAnimatingCopy: false)
            .environment(\.colorScheme, colorScheme)
            .frame(width: 22, height: 18)
    )
    view.frame = NSRect(x: 0, y: 0, width: 22, height: 18)
    view.layoutSubtreeIfNeeded()

    let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds)!
    view.cacheDisplay(in: view.bounds, to: rep)

    var bright = 0
    var dark = 0
    var visible = 0
    for y in 0..<rep.pixelsHigh {
        for x in 0..<rep.pixelsWide {
            guard let color = rep.colorAt(x: x, y: y),
                  color.alphaComponent > 0.05 else { continue }
            visible += 1
            let luminance = (color.redComponent + color.greenComponent + color.blueComponent) / 3
            if luminance > 0.6 {
                bright += 1
            }
            if luminance < 0.4 {
                dark += 1
            }
        }
    }
    return PixelToneStats(bright: bright, dark: dark, visible: visible)
}

@main
struct MenuBarIconContract {
    @MainActor
    static func main() {
        let steady = ClipfanMenuBarIconArtwork.steadyLabelImage()
        require(steady.isTemplate, "steady menu bar label image must be a template image")
        require(steady.size == NSSize(width: 22, height: 18), "steady menu bar label image has wrong size: \(steady.size)")
        let steadyCoverage = visiblePixelCoverage(steady)
        require(steadyCoverage > 0.08, "steady menu bar label image has too little visible artwork")
        let steadyMeanAlpha = meanAlpha(steady)
        require(steadyMeanAlpha > 0.28, "steady menu bar label image has too little card face")
        require(steadyMeanAlpha < 0.44, "steady menu bar label image is too visually dense")

        let card = ClipfanMenuBarIconArtwork.animatedCardImage()
        require(card.isTemplate, "animated menu bar card image must be a template image")
        require(card.size.width > 0 && card.size.height > 0, "animated menu bar card image must have non-zero size")
        require(
            alphaAt(card, x: card.size.width / 2, y: card.size.height / 2) > 0.15,
            "animated menu bar card image must include a visible card face"
        )
        let cardCoverage = visiblePixelCoverage(card)
        require(cardCoverage > 0.45, "animated menu bar card image has too little visible card face")
        require(cardCoverage < 0.98, "animated menu bar card image fills too much of its frame")

        let front = ClipfanMenuBarIconArtwork.frontCardImage()
        require(
            alphaAt(front, x: 14, y: 10) > 0.15,
            "front menu bar card image must include a visible card face"
        )

        let lightStats = renderedToneStats(colorScheme: .light)
        require(lightStats.visible > 0, "light mode menu bar label must render visible artwork")
        require(lightStats.dark > lightStats.visible * 8 / 10, "light mode menu bar label must render dark template artwork")

        let darkStats = renderedToneStats(colorScheme: .dark)
        require(darkStats.visible > 0, "dark mode menu bar label must render visible artwork")
        require(darkStats.bright > darkStats.visible * 8 / 10, "dark mode menu bar label must render light template artwork")

        let startFrames = MenuBarFanInsertTimeline.frames(progress: 0)
        let earlyFrames = MenuBarFanInsertTimeline.frames(progress: 0.10)
        let midFrames = MenuBarFanInsertTimeline.frames(progress: 0.50)
        let endFrames = MenuBarFanInsertTimeline.frames(progress: 1)
        require(startFrames.count == 4, "copy animation must render four cards")

        let incomingStart = frame(startFrames, id: .incoming).slot
        let incomingEarly = frame(earlyFrames, id: .incoming).slot
        let incomingMid = frame(midFrames, id: .incoming).slot
        let incomingEnd = frame(endFrames, id: .incoming).slot
        require(incomingStart.offsetX > MenuBarFanCardSlot.front.offsetX, "incoming card must start to the right of the stack")
        require(incomingEarly.offsetX < incomingStart.offsetX, "incoming card must start sliding before existing cards move")
        require(incomingMid.offsetX < incomingEarly.offsetX, "incoming card must keep sliding through the hand")
        requireClose(incomingEnd.offsetX, MenuBarFanCardSlot.front.offsetX, accuracy: 0.01, "incoming card must finish in the front slot")

        let oldFrontEarly = frame(earlyFrames, id: .oldFront).slot
        requireClose(oldFrontEarly.offsetX, MenuBarFanCardSlot.front.offsetX, accuracy: 0.01, "old front card must not move at the same time as incoming card")

        let oldFrontMid = frame(midFrames, id: .oldFront).slot
        require(oldFrontMid.offsetX < MenuBarFanCardSlot.front.offsetX, "old front card must move toward the middle after incoming starts")
        let oldFrontEnd = frame(endFrames, id: .oldFront).slot
        requireClose(oldFrontEnd.offsetX, MenuBarFanCardSlot.middle.offsetX, accuracy: 0.01, "old front card must finish in the middle slot")

        let oldMiddleEnd = frame(endFrames, id: .oldMiddle).slot
        requireClose(oldMiddleEnd.offsetX, MenuBarFanCardSlot.back.offsetX, accuracy: 0.01, "old middle card must finish in the back slot")

        let oldBackMid = frame(midFrames, id: .oldBack).slot
        let oldBackEnd = frame(endFrames, id: .oldBack).slot
        require(oldBackEnd.offsetX < oldBackMid.offsetX, "old back card must exit left at the end of the animation")
    }
}
SWIFT

swiftc -parse-as-library \
  "$repo/apps/mac/Clipfan/Sources/Clipfan/MenuBarIcon.swift" \
  "$tmp/menubar_icon_contract.swift" \
  -o "$tmp/menubar_icon_contract"

"$tmp/menubar_icon_contract"
