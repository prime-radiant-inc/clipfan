import SwiftUI

struct HistoryRow: View {
    let entry: HistoryEntry

    var body: some View {
        HStack(spacing: 10) {
            thumbnail
                .frame(width: 30, height: 30)
                .clipShape(RoundedRectangle(cornerRadius: 6))
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.preview)
                    .lineLimit(1)
                    .font(.system(size: 13))
                HStack(spacing: 6) {
                    Text(entry.kind.rawValue)
                    Text(entry.origin)
                        .padding(.horizontal, 5)
                        .background(Color.secondary.opacity(0.2))
                        .clipShape(Capsule())
                    Text(entry.ts, style: .relative)
                }
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            }
            Spacer()
            if entry.pinned {
                Image(systemName: "pin.fill").font(.system(size: 10)).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 3)
    }

    @ViewBuilder private var thumbnail: some View {
        if entry.kind == .image, let path = entry.imagePath,
           let img = NSImage(contentsOfFile: path) {
            Image(nsImage: img).resizable().scaledToFill()
        } else {
            ZStack {
                Color.secondary.opacity(0.18)
                Image(systemName: entry.kind == .link ? "link" : "doc.text")
                    .font(.system(size: 13))
                    .foregroundStyle(.secondary)
            }
        }
    }
}
