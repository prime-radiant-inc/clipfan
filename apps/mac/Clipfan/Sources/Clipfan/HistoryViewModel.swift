import Foundation

enum TypeFilter: String, CaseIterable, Identifiable {
    case all, text, image, link
    var id: String { rawValue }
    var label: String {
        switch self {
        case .all:   return "All"
        case .text:  return "Text"
        case .image: return "Image"
        case .link:  return "Link"
        }
    }
}

/// filteredHistory applies the search string and type filter, then floats
/// pinned items to the top (newest-first within the pinned and unpinned
/// groups). Pure function — unit-tested, no SwiftUI dependency.
func filteredHistory(_ entries: [HistoryEntry], search: String, typeFilter: TypeFilter) -> [HistoryEntry] {
    let q = search.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    let matched = entries.filter { e in
        let typeOK = typeFilter == .all || e.kind.rawValue == typeFilter.rawValue
        guard typeOK else { return false }
        guard !q.isEmpty else { return true }
        let hay = (e.preview + " " + (e.text ?? "")).lowercased()
        return hay.contains(q)
    }
    return matched.sorted { a, b in
        if a.pinned != b.pinned { return a.pinned }
        return a.ts > b.ts
    }
}
