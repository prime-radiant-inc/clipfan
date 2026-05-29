import Foundation

/// movedSelection returns the id `delta` positions from the current selection
/// within `list`, clamped to the ends. A nil current selection (or a selection
/// no longer present) starts at the first item. Returns nil only for an empty list.
func movedSelection(from current: HistoryEntry.ID?, in list: [HistoryEntry], delta: Int) -> HistoryEntry.ID? {
    guard !list.isEmpty else { return nil }
    guard let current, let idx = list.firstIndex(where: { $0.id == current }) else {
        return list.first?.id
    }
    let next = max(0, min(list.count - 1, idx + delta))
    return list[next].id
}

/// idForNumber maps a 1-based quick-paste number (⌘1…⌘9) to the id at that
/// position, or nil if out of range.
func idForNumber(_ n: Int, in list: [HistoryEntry]) -> HistoryEntry.ID? {
    guard n >= 1, n <= list.count else { return nil }
    return list[n - 1].id
}

/// clampedSelection returns `current` if it still exists in `list`, otherwise
/// the first item's id (nil for an empty list). Used after the list refreshes.
func clampedSelection(_ current: HistoryEntry.ID?, in list: [HistoryEntry]) -> HistoryEntry.ID? {
    if let current, list.contains(where: { $0.id == current }) { return current }
    return list.first?.id
}
