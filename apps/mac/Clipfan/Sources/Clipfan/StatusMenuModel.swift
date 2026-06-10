/// Top-level commands shown before the fleet status rows.
enum StatusMenuCommand: CaseIterable, Identifiable {
    case openClipboard
    case installUpdate
    case settings
    case quit

    var id: Self { self }

    var title: String {
        switch self {
        case .openClipboard: return "Open Clipboard"
        case .installUpdate: return "Install Update…"
        case .settings:      return "Settings…"
        case .quit:          return "Quit"
        }
    }

    func shortcut(toggleShortcutLabel: String) -> String {
        switch self {
        case .openClipboard: return toggleShortcutLabel
        case .installUpdate: return ""
        case .settings:      return "⌘,"
        case .quit:          return "⌘Q"
        }
    }
}

struct StatusMenuCommandRow: Equatable {
    let command: StatusMenuCommand
    let title: String
    let shortcut: String
}

extension StatusMenuCommandRow: Identifiable {
    var id: StatusMenuCommand { command }
}

func statusMenuCommandRows(toggleShortcutLabel: String, appUpdateAvailable: Bool = false) -> [StatusMenuCommandRow] {
    StatusMenuCommand.allCases.compactMap { command in
        if command == .installUpdate && !appUpdateAvailable {
            return nil
        }
        return StatusMenuCommandRow(
            command: command,
            title: command.title,
            shortcut: command.shortcut(toggleShortcutLabel: toggleShortcutLabel)
        )
    }
}

struct AppUpdateAvailability {
    private(set) var isUpdateAvailable = false
    private var startupRecommendationPending = false

    mutating func prepareStartupRecommendation() {
        startupRecommendationPending = true
    }

    mutating func noteUpdateFound() {
        isUpdateAvailable = true
    }

    mutating func noteUpdateNotFound() {
        isUpdateAvailable = false
    }

    mutating func finishStartupProbeShouldRecommend() -> Bool {
        defer { startupRecommendationPending = false }
        return startupRecommendationPending && isUpdateAvailable
    }
}
