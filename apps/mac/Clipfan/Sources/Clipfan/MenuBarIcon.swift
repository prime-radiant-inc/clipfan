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

    static func stackImage() -> NSImage {
        makeImage { context in
            MenuBarFanCardSlot.steady.forEach { drawCard(context, slot: $0) }
        }
    }

    static func frontCardImage() -> NSImage {
        makeImage { context in
            drawCard(context, slot: .front)
        }
    }

    private static func makeImage(_ draw: @escaping (CGContext) -> Void) -> NSImage {
        let image = NSImage(size: iconSize, flipped: false) { _ in
            guard let context = NSGraphicsContext.current?.cgContext else { return false }
            draw(context)
            return true
        }
        image.isTemplate = true
        return image
    }

    private static func drawCard(_ context: CGContext, slot: MenuBarFanCardSlot) {
        context.saveGState()
        context.translateBy(x: iconSize.width / 2 + slot.offsetX,
                            y: iconSize.height - slot.topY - MenuBarFanCardSlot.size.height / 2)
        context.rotate(by: slot.rotation * .pi / 180)
        context.scaleBy(x: slot.scale, y: slot.scale)
        let rect = CGRect(x: -MenuBarFanCardSlot.size.width / 2,
                          y: -MenuBarFanCardSlot.size.height / 2,
                          width: MenuBarFanCardSlot.size.width,
                          height: MenuBarFanCardSlot.size.height)
        let path = CGPath(roundedRect: rect,
                          cornerWidth: MenuBarFanCardSlot.cornerRadius,
                          cornerHeight: MenuBarFanCardSlot.cornerRadius,
                          transform: nil)
        context.addPath(path)
        context.setFillColor(NSColor.black.withAlphaComponent(slot.opacity).cgColor)
        context.fillPath()
        context.addPath(path)
        context.setStrokeColor(NSColor.black.withAlphaComponent(0.88).cgColor)
        context.setLineWidth(MenuBarFanCardSlot.strokeWidth)
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
