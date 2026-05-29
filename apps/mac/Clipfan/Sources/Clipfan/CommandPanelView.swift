import SwiftUI

struct CommandPanelView: View {
    @ObservedObject var daemon: DaemonClient
    /// Called after a paste so the controller can dismiss the panel.
    var onPaste: () -> Void
    /// Called when the user presses Esc.
    var onDismiss: () -> Void

    @State private var search = ""
    @State private var filter: TypeFilter = .all
    @State private var selection: HistoryEntry.ID?
    @FocusState private var searchFocused: Bool

    private var items: [HistoryEntry] {
        filteredHistory(daemon.history, search: search, typeFilter: filter)
    }
    private var selected: HistoryEntry? {
        items.first { $0.id == selection } ?? items.first
    }

    var body: some View {
        VStack(spacing: 0) {
            searchRow
            Divider()
            if items.isEmpty {
                emptyState
            } else {
                HStack(spacing: 0) {
                    listPane.frame(width: 300)
                    Divider()
                    previewPane.frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            Divider()
            footer
        }
        .frame(width: 660, height: 440)
        .background(VisualEffectBackground())
        .background(hiddenShortcuts)
        .task {
            await daemon.refreshHistory()
            searchFocused = true
        }
        .onChange(of: items.map(\.id)) { ids in
            selection = clampedSelection(selection, in: items)
        }
    }

    // MARK: search

    private var searchRow: some View {
        HStack(spacing: 10) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Type to search clipboard…", text: $search)
                .textFieldStyle(.plain)
                .font(.system(size: 15))
                .focused($searchFocused)
                .onSubmit { pasteSelected() }
            Picker("", selection: $filter) {
                ForEach(TypeFilter.allCases) { f in Text(f.label).tag(f) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 240)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
    }

    // MARK: list

    private var listPane: some View {
        List(selection: $selection) {
            ForEach(Array(items.enumerated()), id: \.element.id) { idx, e in
                CommandRow(entry: e, number: idx < 9 ? idx + 1 : nil)
                    .tag(e.id)
                    .contextMenu {
                        Button(e.pinned ? "Unpin" : "Pin") {
                            Task { await daemon.setPinned(e.id, !e.pinned) }
                        }
                        Button("Delete", role: .destructive) {
                            Task { await daemon.deleteEntry(e.id) }
                        }
                    }
            }
        }
        .listStyle(.plain)
    }

    // MARK: preview

    @ViewBuilder private var previewPane: some View {
        if let e = selected {
            VStack(alignment: .leading, spacing: 0) {
                Group {
                    if e.kind == .image, let p = e.imagePath, let img = NSImage(contentsOfFile: p) {
                        Image(nsImage: img).resizable().scaledToFit().padding(16)
                    } else {
                        ScrollView {
                            Text(e.text ?? e.preview)
                                .font(.system(size: 13,
                                               design: isMonospacePreferred(e.text ?? e.preview) ? .monospaced : .default))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(16)
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider()
                previewBadge(e).padding(.horizontal, 16).padding(.vertical, 10)
            }
        } else {
            Color.clear
        }
    }

    private func previewBadge(_ e: HistoryEntry) -> some View {
        HStack(spacing: 8) {
            Text(metaLine(e))
            if e.pinned { Image(systemName: "pin.fill") }
            Spacer()
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
    }

    private func metaLine(_ e: HistoryEntry) -> String {
        var parts = [e.kind.rawValue]
        if e.kind == .image, let p = e.imagePath, let dims = imageDimensions(path: p) {
            parts.append(dims)
        }
        parts.append(humanSize(e.sizeBytes))
        parts.append("from \(e.origin)")
        return parts.joined(separator: " · ")
    }

    // MARK: footer + empty

    private var footer: some View {
        HStack(spacing: 10) {
            Text("\(items.count) of \(daemon.history.count)")
            Spacer()
            keyHint("return", "Paste")
            keyHint("command", "1–9 quick")
            keyHint("escape", "Close")
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private func keyHint(_ symbol: String, _ label: String) -> some View {
        HStack(spacing: 4) {
            Image(systemName: symbol == "return" ? "return" :
                              symbol == "escape" ? "escape" : "command")
            Text(label)
        }
    }

    private var emptyState: some View {
        VStack(spacing: 8) {
            Image(systemName: "doc.on.clipboard")
                .font(.system(size: 34))
                .foregroundStyle(.tertiary)
            Text(search.isEmpty ? "No clipboard history yet" : "No matches")
                .foregroundStyle(.secondary)
            if search.isEmpty {
                Text("Copy something on any host to get started")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: keyboard

    /// Hidden buttons carrying ⏎, Esc, and ⌘1–9 so the whole panel is keyboard-driven.
    private var hiddenShortcuts: some View {
        ZStack {
            Button("") { pasteSelected() }
                .keyboardShortcut(.return, modifiers: [])
            Button("") { onDismiss() }
                .keyboardShortcut(.cancelAction)
            Button("") { selection = movedSelection(from: selection, in: items, delta: 1) }
                .keyboardShortcut(.downArrow, modifiers: [])
            Button("") { selection = movedSelection(from: selection, in: items, delta: -1) }
                .keyboardShortcut(.upArrow, modifiers: [])
            ForEach(1...9, id: \.self) { n in
                Button("") { pasteNumber(n) }
                    .keyboardShortcut(KeyEquivalent(Character("\(n)")), modifiers: .command)
            }
        }
        .opacity(0)
        .allowsHitTesting(false)
    }

    private func pasteSelected() {
        guard let id = selection ?? items.first?.id else { return }
        Task { await daemon.restore(id); onPaste() }
    }

    private func pasteNumber(_ n: Int) {
        guard let id = idForNumber(n, in: items) else { return }
        Task { await daemon.restore(id); onPaste() }
    }
}

/// One comfortable row: thumbnail · title · metadata · ⌘-number.
struct CommandRow: View {
    let entry: HistoryEntry
    let number: Int?

    var body: some View {
        HStack(spacing: 11) {
            thumbnail
                .frame(width: 32, height: 32)
                .clipShape(RoundedRectangle(cornerRadius: 7))
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.preview)
                    .lineLimit(1)
                    .font(.system(size: 13,
                                  design: isMonospacePreferred(entry.preview) ? .monospaced : .default))
                HStack(spacing: 6) {
                    Text(entry.kind.rawValue)
                    Text(entry.origin)
                        .padding(.horizontal, 6)
                        .background(Color.secondary.opacity(0.18))
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
            if let number {
                Text("⌘\(number)")
                    .font(.system(size: 10))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background(Color.secondary.opacity(0.12))
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            }
        }
        .padding(.vertical, 5)
    }

    @ViewBuilder private var thumbnail: some View {
        if entry.kind == .image, let path = entry.imagePath,
           let img = NSImage(contentsOfFile: path) {
            Image(nsImage: img).resizable().scaledToFill()
        } else {
            ZStack {
                Color.secondary.opacity(0.15)
                Image(systemName: entry.kind == .link ? "link" : "doc.text")
                    .font(.system(size: 13))
                    .foregroundStyle(.secondary)
            }
        }
    }
}
