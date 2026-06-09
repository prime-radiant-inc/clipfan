import AppKit
import SwiftUI

struct MenuBarCopyAnimationTracker {
    private var isPrimed = false
    private var lastHistoryID: String?

    mutating func seedInitialHistory(_ historyID: String?) {
        guard !isPrimed else { return }
        lastHistoryID = historyID
        isPrimed = true
    }

    mutating func shouldAnimate(latestHistoryID: String?) -> Bool {
        guard isPrimed else {
            lastHistoryID = latestHistoryID
            return false
        }
        guard let latestHistoryID else {
            lastHistoryID = nil
            return false
        }
        defer { lastHistoryID = latestHistoryID }
        return latestHistoryID != lastHistoryID
    }
}

enum ClipfanMenuBarIconArtwork {
    private static let iconSize = NSSize(width: 22, height: 18)
    static let appIconCardSlots = MenuBarFanCardSlot.steady

    static func stackImage() -> NSImage {
        makeImage { context in
            MenuBarFanCardSlot.steady.forEach { drawMenuBarCard(context, slot: $0) }
        }
    }

    static func frontCardImage() -> NSImage {
        makeImage { context in
            drawMenuBarCard(context, slot: .front)
        }
    }

    static func appIconImage(size: CGFloat) -> NSImage {
        makeImage(size: NSSize(width: size, height: size), isTemplate: false) { context in
            drawAppIconBackground(context, size: size)
            drawAppIconCards(context, size: size)
        }
    }

    private static func makeImage(
        size: NSSize = iconSize,
        isTemplate: Bool = true,
        _ draw: @escaping (CGContext) -> Void
    ) -> NSImage {
        let image = NSImage(size: size, flipped: false) { _ in
            guard let context = NSGraphicsContext.current?.cgContext else { return false }
            draw(context)
            return true
        }
        image.isTemplate = isTemplate
        return image
    }

    private static func drawMenuBarCard(_ context: CGContext, slot: MenuBarFanCardSlot) {
        let topCenter = CGPoint(
            x: iconSize.width / 2 + slot.offsetX,
            y: iconSize.height - slot.topY
        )
        drawCard(
            context,
            slot: slot,
            topCenter: topCenter,
            size: MenuBarFanCardSlot.size,
            cornerRadius: MenuBarFanCardSlot.cornerRadius,
            fillColor: NSColor.black.withAlphaComponent(slot.opacity),
            strokeColor: NSColor.black.withAlphaComponent(0.88),
            strokeWidth: MenuBarFanCardSlot.strokeWidth
        )
    }

    private static func drawAppIconBackground(_ context: CGContext, size: CGFloat) {
        let rect = CGRect(x: 0, y: 0, width: size, height: size)
        let scale = size / 1024
        let backgroundPath = CGPath(
            roundedRect: rect.insetBy(dx: 80 * scale, dy: 80 * scale),
            cornerWidth: 180 * scale,
            cornerHeight: 180 * scale,
            transform: nil
        )
        context.addPath(backgroundPath)
        let colors = [
            NSColor(calibratedWhite: 0.22, alpha: 1).cgColor,
            NSColor(calibratedWhite: 0.12, alpha: 1).cgColor,
        ] as CFArray
        let gradient = CGGradient(
            colorsSpace: CGColorSpaceCreateDeviceRGB(),
            colors: colors,
            locations: [0, 1]
        )!
        context.saveGState()
        context.clip()
        context.drawLinearGradient(
            gradient,
            start: CGPoint(x: 0, y: size),
            end: CGPoint(x: size, y: 0),
            options: []
        )
        context.restoreGState()
    }

    private static func drawAppIconCards(_ context: CGContext, size: CGFloat) {
        let layoutScale = size * 0.60 / iconSize.width
        let layoutSize = CGSize(width: iconSize.width * layoutScale, height: iconSize.height * layoutScale)
        let layoutFrame = CGRect(
            x: (size - layoutSize.width) / 2,
            y: (size - layoutSize.height) / 2 - size * 0.01,
            width: layoutSize.width,
            height: layoutSize.height
        )
        let fills = [
            NSColor(calibratedWhite: 0.78, alpha: 1),
            NSColor(calibratedWhite: 0.92, alpha: 1),
            NSColor(calibratedRed: 0.035, green: 0.52, blue: 0.98, alpha: 1),
        ]

        for (slot, fill) in zip(appIconCardSlots, fills) {
            let topCenter = CGPoint(
                x: layoutFrame.minX + (iconSize.width / 2 + slot.offsetX) * layoutScale,
                y: layoutFrame.maxY - slot.topY * layoutScale
            )
            drawCard(
                context,
                slot: slot,
                topCenter: topCenter,
                size: CGSize(
                    width: MenuBarFanCardSlot.size.width * layoutScale,
                    height: MenuBarFanCardSlot.size.height * layoutScale
                ),
                cornerRadius: MenuBarFanCardSlot.cornerRadius * layoutScale,
                fillColor: fill,
                strokeColor: NSColor.black.withAlphaComponent(0.14),
                strokeWidth: max(1, size * 0.004)
            )
        }
    }

    private static func drawCard(
        _ context: CGContext,
        slot: MenuBarFanCardSlot,
        topCenter: CGPoint,
        size: CGSize,
        cornerRadius: CGFloat,
        fillColor: NSColor,
        strokeColor: NSColor,
        strokeWidth: CGFloat
    ) {
        context.saveGState()
        context.translateBy(x: topCenter.x, y: topCenter.y)
        context.rotate(by: slot.rotation * .pi / 180)
        context.scaleBy(x: slot.scale, y: slot.scale)
        let rect = CGRect(x: -size.width / 2, y: -size.height, width: size.width, height: size.height)
        let path = CGPath(roundedRect: rect,
                          cornerWidth: cornerRadius,
                          cornerHeight: cornerRadius,
                          transform: nil)
        context.addPath(path)
        context.setFillColor(fillColor.cgColor)
        context.fillPath()
        context.addPath(path)
        context.setStrokeColor(strokeColor.cgColor)
        context.setLineWidth(strokeWidth)
        context.strokePath()
        context.restoreGState()
    }
}

struct MenuBarFanInsertTiming {
    let duration: Double
    let frontDelay: Double
    let middleDelay: Double
    let backDelay: Double

    static let quickMenuBar = MenuBarFanInsertTiming(
        duration: 0.82,
        frontDelay: 0.13,
        middleDelay: 0.26,
        backDelay: 0.43
    )

    var dismissDelayNanoseconds: UInt64 {
        UInt64((duration + 0.08) * 1_000_000_000)
    }

    func animation(delay: Double) -> Animation {
        .timingCurve(0.24, 0.86, 0.2, 1, duration: max(0.12, duration - delay)).delay(delay)
    }
}

struct MenuBarFanCardSlot: Hashable {
    static let size = CGSize(width: 11, height: 13)
    static let cornerRadius: CGFloat = 2.2
    static let strokeWidth: CGFloat = 1.1

    let offsetX: CGFloat
    let topY: CGFloat
    let rotation: CGFloat
    let opacity: Double
    let scale: CGFloat
    let zIndex: Double

    static let topY: CGFloat = 2.4

    static let back = MenuBarFanCardSlot(offsetX: -4.0, topY: topY, rotation: -13, opacity: 0.36, scale: 0.94, zIndex: 1)
    static let middle = MenuBarFanCardSlot(offsetX: -0.8, topY: topY, rotation: -1, opacity: 0.62, scale: 0.97, zIndex: 2)
    static let front = MenuBarFanCardSlot(offsetX: 3.0, topY: topY, rotation: 11, opacity: 1.0, scale: 1.0, zIndex: 3)
    static let incoming = MenuBarFanCardSlot(offsetX: 14.5, topY: topY, rotation: 20, opacity: 0, scale: 0.98, zIndex: 4)
    static let discarded = MenuBarFanCardSlot(offsetX: -10.0, topY: topY, rotation: -22, opacity: 0, scale: 0.90, zIndex: 0)

    static let steady = [back, middle, front]
}

struct ClipfanMenuBarIcon: View {
    let isAnimatingCopy: Bool
    var animationGeneration = 0
    var timing = MenuBarFanInsertTiming.quickMenuBar

    var body: some View {
        ZStack {
            if isAnimatingCopy {
                ClipfanMenuBarFanInsertIcon(timing: timing)
                    .id(animationGeneration)
            } else {
                ClipfanMenuBarStaticIcon()
            }
        }
        .frame(width: 22, height: 18)
        .accessibilityLabel("Clipfan")
    }
}

private struct ClipfanMenuBarStaticIcon: View {
    var body: some View {
        ZStack {
            ForEach(Array(MenuBarFanCardSlot.steady.enumerated()), id: \.offset) { _, slot in
                MenuBarFanCardView(slot: slot)
            }
        }
    }
}

private struct ClipfanMenuBarFanInsertIcon: View {
    let timing: MenuBarFanInsertTiming
    @State private var inserted = false

    var body: some View {
        ZStack {
            MenuBarFanCardView(slot: inserted ? .discarded : .back)
                .animation(timing.animation(delay: timing.backDelay), value: inserted)
            MenuBarFanCardView(slot: inserted ? .back : .middle)
                .animation(timing.animation(delay: timing.middleDelay), value: inserted)
            MenuBarFanCardView(slot: inserted ? .middle : .front)
                .animation(timing.animation(delay: timing.frontDelay), value: inserted)
            MenuBarFanCardView(slot: inserted ? .front : .incoming)
                .animation(timing.animation(delay: 0), value: inserted)
        }
        .onAppear {
            inserted = false
            DispatchQueue.main.async {
                inserted = true
            }
        }
    }
}

private struct MenuBarFanCardView: View {
    let slot: MenuBarFanCardSlot

    var body: some View {
        RoundedRectangle(cornerRadius: MenuBarFanCardSlot.cornerRadius, style: .continuous)
            .fill(.primary.opacity(slot.opacity))
            .overlay(
                RoundedRectangle(cornerRadius: MenuBarFanCardSlot.cornerRadius, style: .continuous)
                    .stroke(.primary.opacity(0.88), lineWidth: MenuBarFanCardSlot.strokeWidth)
            )
            .frame(width: MenuBarFanCardSlot.size.width, height: MenuBarFanCardSlot.size.height)
            .scaleEffect(slot.scale, anchor: UnitPoint(x: 0.5, y: 0))
            .rotationEffect(.degrees(slot.rotation), anchor: UnitPoint(x: 0.5, y: 0))
            .position(x: 11 + slot.offsetX, y: slot.topY + MenuBarFanCardSlot.size.height / 2)
            .zIndex(slot.zIndex)
    }
}
