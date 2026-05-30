import KeyboardShortcuts

extension KeyboardShortcuts.Name {
    /// Global show/hide for the clipboard window. Defaults to ⇧⌘V.
    static let toggleClipboard = Self("toggleClipboard",
        default: .init(.v, modifiers: [.command, .shift]))
}
