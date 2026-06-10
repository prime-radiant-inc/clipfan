#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/app_update_menu_contract.swift" <<'SWIFT'
import Foundation

func require(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() {
        fputs(message + "\n", stderr)
        exit(1)
    }
}

@main
struct AppUpdateMenuContract {
    static func main() {
        let command = "\u{2318}"
        let ellipsis = "\u{2026}"

        let dailyRows = statusMenuCommandRows(toggleShortcutLabel: "Command-Shift-V")
        require(dailyRows == [
            StatusMenuCommandRow(command: .openClipboard, title: "Open Clipboard", shortcut: "Command-Shift-V"),
            StatusMenuCommandRow(command: .settings, title: "Settings\(ellipsis)", shortcut: "\(command),"),
            StatusMenuCommandRow(command: .quit, title: "Quit", shortcut: "\(command)Q"),
        ], "daily menu rows must stay focused on clipboard, settings, and quit")

        let updateRows = statusMenuCommandRows(toggleShortcutLabel: "Command-Shift-V", appUpdateAvailable: true)
        require(updateRows == [
            StatusMenuCommandRow(command: .openClipboard, title: "Open Clipboard", shortcut: "Command-Shift-V"),
            StatusMenuCommandRow(command: .installUpdate, title: "Install Update\(ellipsis)", shortcut: ""),
            StatusMenuCommandRow(command: .settings, title: "Settings\(ellipsis)", shortcut: "\(command),"),
            StatusMenuCommandRow(command: .quit, title: "Quit", shortcut: "\(command)Q"),
        ], "install update row must appear between clipboard and settings when an update is available")

        var availability = AppUpdateAvailability()
        availability.prepareStartupRecommendation()
        require(!availability.finishStartupProbeShouldRecommend(), "startup probe must not recommend when no update was found")

        availability.prepareStartupRecommendation()
        availability.noteUpdateFound()
        require(availability.finishStartupProbeShouldRecommend(), "startup probe must recommend after finding an update")
        require(!availability.finishStartupProbeShouldRecommend(), "startup recommendation must be consumed once")

        availability.noteUpdateNotFound()
        require(!availability.isUpdateAvailable, "missing update result must clear update availability")
    }
}
SWIFT

swiftc -parse-as-library \
  "$repo/apps/mac/Clipfan/Sources/Clipfan/StatusMenuModel.swift" \
  "$tmp/app_update_menu_contract.swift" \
  -o "$tmp/app_update_menu_contract"

"$tmp/app_update_menu_contract"
