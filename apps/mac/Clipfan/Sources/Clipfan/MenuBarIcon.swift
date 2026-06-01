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

struct ClipfanMenuBarIcon: View {
    let isAnimatingCopy: Bool

    var body: some View {
        ZStack {
            card(width: 11, height: 13, cornerRadius: 2.2, opacity: 0.36)
                .rotationEffect(.degrees(-12))
                .offset(x: -3.5, y: 1.5)
            card(width: 11, height: 13, cornerRadius: 2.2, opacity: 0.62)
                .rotationEffect(.degrees(0))
                .offset(x: -0.5, y: 0)
            card(width: 11, height: 13, cornerRadius: 2.2, opacity: 1.0)
                .rotationEffect(.degrees(11))
                .offset(x: 3.2, y: -0.3)

            if isAnimatingCopy {
                card(width: 11, height: 13, cornerRadius: 2.2, opacity: 1.0)
                    .rotationEffect(.degrees(11))
                    .offset(x: 3.2, y: -0.3)
                    .transition(.asymmetric(
                        insertion: .offset(x: -7, y: -8).combined(with: .opacity),
                        removal: .opacity
                    ))
            }
        }
        .foregroundStyle(.primary)
        .frame(width: 22, height: 18)
        .accessibilityLabel("Clipfan")
    }

    private func card(width: CGFloat, height: CGFloat, cornerRadius: CGFloat, opacity: Double) -> some View {
        RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
            .fill(.primary.opacity(opacity))
            .overlay {
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .stroke(.primary.opacity(0.88), lineWidth: 1.1)
            }
            .frame(width: width, height: height)
    }
}

