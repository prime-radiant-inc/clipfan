import SwiftUI

struct HistoryWindow: View {
    @ObservedObject var daemon: DaemonClient
    @State private var search = ""
    @State private var filter: TypeFilter = .all
    @State private var selection: HistoryEntry.ID?

    private var items: [HistoryEntry] {
        filteredHistory(daemon.history, search: search, typeFilter: filter)
    }
    private var selected: HistoryEntry? {
        items.first { $0.id == selection } ?? items.first
    }

    var body: some View {
        VStack(spacing: 0) {
            searchBar
            Divider()
            HStack(spacing: 0) {
                listPane.frame(width: 260)
                Divider()
                previewPane.frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(width: 640, height: 420)
        .task { await daemon.refreshHistory() }
        .onChange(of: items.map(\.id)) { ids in
            if selection == nil || !(ids.contains(selection!)) {
                selection = ids.first
            }
        }
    }

    private var searchBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Search clipboard…", text: $search)
                .textFieldStyle(.plain)
            Picker("", selection: $filter) {
                ForEach(TypeFilter.allCases) { f in Text(f.label).tag(f) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 240)
        }
        .padding(10)
    }

    private var listPane: some View {
        List(selection: $selection) {
            ForEach(items) { e in
                HistoryRow(entry: e)
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
        .listStyle(.sidebar)
    }

    @ViewBuilder private var previewPane: some View {
        if let e = selected {
            VStack(alignment: .leading, spacing: 0) {
                Group {
                    if e.kind == .image, let p = e.imagePath, let img = NSImage(contentsOfFile: p) {
                        Image(nsImage: img).resizable().scaledToFit().padding()
                    } else {
                        ScrollView {
                            Text(e.text ?? e.preview)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding()
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider()
                footer(e)
            }
        } else {
            Text("No clipboard history")
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func footer(_ e: HistoryEntry) -> some View {
        HStack(spacing: 8) {
            Text(e.kind.rawValue)
            Text("\(e.sizeBytes) B")
            Text("from \(e.origin)")
            Text(e.ts, style: .relative)
            Spacer()
            Button("Paste") { Task { await daemon.restore(e.id) } }
                .keyboardShortcut(.defaultAction)
        }
        .font(.system(size: 11))
        .foregroundStyle(.secondary)
        .padding(10)
    }
}
