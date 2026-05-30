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
            Divider().opacity(0.5)
            if items.isEmpty {
                emptyState
            } else {
                HStack(spacing: 0) {
                    listPane.frame(width: 320)
                    Divider().opacity(0.5)
                    previewPane.frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            Divider().opacity(0.5)
            footer
        }
        .frame(width: 680, height: 460)
        .background(VisualEffectBackground(material: .windowBackground))
        .overlay { hiddenShortcuts.frame(width: 0, height: 0).clipped() }
        .task {
            searchFocused = true
            await daemon.refreshHistory()
        }
        .onChange(of: items.map(\.id)) { _ in
            selection = clampedSelection(selection, in: items)
        }
    }

    // MARK: search

    private var searchRow: some View {
        HStack(spacing: 10) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 14))
                .foregroundStyle(.secondary)
            TextField("Type to search clipboard…", text: $search)
                .textFieldStyle(.plain)
                .font(.system(size: 16))
                .focused($searchFocused)
                .onSubmit { pasteSelected() }
            Picker("", selection: $filter) {
                ForEach(TypeFilter.allCases) { f in Text(f.label).tag(f) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 13)
    }

    // MARK: list

    private var listPane: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 2) {
                    ForEach(Array(items.enumerated()), id: \.element.id) { idx, e in
                        CommandRow(entry: e,
                                   number: idx < 9 ? idx + 1 : nil,
                                   isSelected: e.id == selection)
                            .id(e.id)
                            .contentShape(Rectangle())
                            .onTapGesture { selection = e.id }
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
                .padding(.horizontal, 8)
                .padding(.vertical, 8)
            }
            .scrollContentBackground(.hidden)
            .onChange(of: selection) { id in
                guard let id else { return }
                withAnimation(.easeOut(duration: 0.12)) { proxy.scrollTo(id, anchor: .center) }
            }
        }
    }

    // MARK: preview

    @ViewBuilder private var previewPane: some View {
        if let e = selected {
            VStack(alignment: .leading, spacing: 0) {
                Group {
                    if e.kind == .image, let p = e.imagePath, let img = NSImage(contentsOfFile: p) {
                        Image(nsImage: img).resizable().scaledToFit().padding(18)
                    } else {
                        ScrollView {
                            Text(e.text ?? e.preview)
                                .font(.system(size: 13,
                                               design: isMonospacePreferred(e.text ?? e.preview) ? .monospaced : .default))
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(18)
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider().opacity(0.5)
                previewBadge(e).padding(.horizontal, 16).padding(.vertical, 11)
            }
        } else {
            Color.clear
        }
    }

    private func previewBadge(_ e: HistoryEntry) -> some View {
        HStack(spacing: 8) {
            Text(metaLine(e))
            if e.pinned {
                Image(systemName: "pin.fill")
                Text("pinned")
            }
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
        HStack(spacing: 14) {
            Text("\(items.count) of \(daemon.history.count)")
            Spacer()
            keyHint("return", "Paste")
            keyHint("number", "⌘1–9")
            keyHint("escape", "Close")
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .padding(.horizontal, 16)
        .padding(.vertical, 9)
    }

    private func keyHint(_ symbol: String, _ label: String) -> some View {
        HStack(spacing: 4) {
            switch symbol {
            case "return": Image(systemName: "return")
            case "escape": Image(systemName: "escape")
            default:       Image(systemName: "command")
            }
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
            Button("") { onDismiss() }
                .keyboardShortcut(.cancelAction)
            Button("") { WindowOpener.shared.openSettings(); onDismiss() }
                .keyboardShortcut(",", modifiers: .command)
            if !items.isEmpty {
                Button("") { pasteSelected() }
                    .keyboardShortcut(.return, modifiers: [])
                Button("") { selection = movedSelection(from: selection, in: items, delta: 1) }
                    .keyboardShortcut(.downArrow, modifiers: [])
                Button("") { selection = movedSelection(from: selection, in: items, delta: -1) }
                    .keyboardShortcut(.upArrow, modifiers: [])
                ForEach(1...9, id: \.self) { n in
                    Button("") { pasteNumber(n) }
                        .keyboardShortcut(KeyEquivalent(Character("\(n)")), modifiers: .command)
                }
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
    let isSelected: Bool

    var body: some View {
        HStack(spacing: 11) {
            thumbnail
                .frame(width: 34, height: 34)
                .clipShape(RoundedRectangle(cornerRadius: 7))
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.preview)
                    .lineLimit(1)
                    .font(.system(size: 13,
                                  design: isMonospacePreferred(entry.preview) ? .monospaced : .default))
                    .foregroundStyle(isSelected ? Color.white : Color.primary)
                HStack(spacing: 6) {
                    Text(entry.kind.rawValue)
                    Text(entry.origin)
                        .lineLimit(1)
                        .fixedSize()
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1)
                        .background(badgeBackground)
                        .clipShape(Capsule())
                    Text(entry.ts, style: .relative)
                        .lineLimit(1)
                }
                .font(.system(size: 11))
                .foregroundStyle(isSelected ? Color.white.opacity(0.85) : Color.secondary)
            }
            Spacer(minLength: 6)
            if entry.pinned {
                Image(systemName: "pin.fill")
                    .font(.system(size: 10))
                    .foregroundStyle(isSelected ? Color.white.opacity(0.85) : Color.secondary)
            }
            if let number {
                Text("⌘\(number)")
                    .font(.system(size: 10, weight: .medium))
                    .foregroundStyle(isSelected ? Color.white.opacity(0.9) : Color.secondary)
                    .padding(.horizontal, 5).padding(.vertical, 1)
                    .background((isSelected ? Color.white.opacity(0.2) : Color.secondary.opacity(0.12)))
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            }
        }
        .padding(.horizontal, 9)
        .padding(.vertical, 7)
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(isSelected ? Color.accentColor : Color.clear)
        )
    }

    private var badgeBackground: Color {
        isSelected ? Color.white.opacity(0.22) : Color.secondary.opacity(0.18)
    }

    @ViewBuilder private var thumbnail: some View {
        if entry.kind == .image, let path = entry.imagePath,
           let img = NSImage(contentsOfFile: path) {
            Image(nsImage: img).resizable().scaledToFill()
        } else {
            ZStack {
                Color.secondary.opacity(0.15)
                Image(systemName: entry.kind == .link ? "link" : "doc.text")
                    .font(.system(size: 14))
                    .foregroundStyle(.secondary)
            }
        }
    }
}
