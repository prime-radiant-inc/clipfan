import Foundation

enum ClipKind: String, Codable {
    case text, image, link
}

struct HistoryEntry: Codable, Identifiable, Equatable {
    let id: String
    let kind: ClipKind
    let preview: String
    let text: String?
    let imagePath: String?
    let sizeBytes: Int
    let origin: String
    let ts: Date
    let pinned: Bool

    enum CodingKeys: String, CodingKey {
        case id, kind, preview, text
        case imagePath = "image_path"
        case sizeBytes = "size_bytes"
        case origin, ts, pinned
    }
}

struct HistoryResponse: Codable {
    let entries: [HistoryEntry]
}
