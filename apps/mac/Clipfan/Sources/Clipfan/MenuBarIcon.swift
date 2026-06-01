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
            drawCard(context, width: 11, height: 13, cornerRadius: 2.2, opacity: 0.36,
                     rotation: -12, offsetX: -3.5, offsetY: 1.5)
            drawCard(context, width: 11, height: 13, cornerRadius: 2.2, opacity: 0.62,
                     rotation: 0, offsetX: -0.5, offsetY: 0)
            drawCard(context, width: 11, height: 13, cornerRadius: 2.2, opacity: 1.0,
                     rotation: 11, offsetX: 3.2, offsetY: -0.3)
        }
    }

    static func frontCardImage() -> NSImage {
        makeImage { context in
            drawCard(context, width: 11, height: 13, cornerRadius: 2.2, opacity: 1.0,
                     rotation: 11, offsetX: 3.2, offsetY: -0.3)
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

    private static func drawCard(_ context: CGContext,
                                 width: CGFloat,
                                 height: CGFloat,
                                 cornerRadius: CGFloat,
                                 opacity: Double,
                                 rotation: CGFloat,
                                 offsetX: CGFloat,
                                 offsetY: CGFloat) {
        context.saveGState()
        context.translateBy(x: iconSize.width / 2 + offsetX,
                            y: iconSize.height / 2 - offsetY)
        context.rotate(by: rotation * .pi / 180)
        let rect = CGRect(x: -width / 2, y: -height / 2, width: width, height: height)
        let path = CGPath(roundedRect: rect, cornerWidth: cornerRadius, cornerHeight: cornerRadius, transform: nil)
        context.addPath(path)
        context.setFillColor(NSColor.black.withAlphaComponent(opacity).cgColor)
        context.fillPath()
        context.addPath(path)
        context.setStrokeColor(NSColor.black.withAlphaComponent(0.88).cgColor)
        context.setLineWidth(1.1)
        context.strokePath()
        context.restoreGState()
    }
}

struct ClipfanMenuBarIcon: View {
    let isAnimatingCopy: Bool

    var body: some View {
        ZStack {
            Image(nsImage: ClipfanMenuBarIconArtwork.stackImage())
                .renderingMode(.template)

            if isAnimatingCopy {
                Image(nsImage: ClipfanMenuBarIconArtwork.frontCardImage())
                    .renderingMode(.template)
                    .transition(.asymmetric(
                        insertion: .offset(x: -7, y: -8).combined(with: .opacity),
                        removal: .opacity
                    ))
            }
        }
        .frame(width: 22, height: 18)
        .accessibilityLabel("Clipfan")
    }
}
