import Foundation
import AppKit

/// humanSize renders a byte count as a short human string ("242 KB").
/// Uses a fixed 1024 base and no decimals so output is deterministic across
/// locales (ByteCountFormatter is locale-dependent and unsuitable for tests).
func humanSize(_ bytes: Int) -> String {
    if bytes < 1024 { return "\(bytes) B" }
    let units = ["KB", "MB", "GB", "TB"]
    var value = Double(bytes) / 1024
    var unit = 0
    while value >= 1024 && unit < units.count - 1 {
        value /= 1024
        unit += 1
    }
    return String(format: "%.0f %@", value, units[unit])
}

/// formatDimensions renders pixel dimensions with a × separator.
func formatDimensions(width: Int, height: Int) -> String {
    "\(width)×\(height)"
}

/// imageDimensions reads a PNG/image file and returns its pixel size as a
/// formatted string, or nil if it can't be read. Thin glue over NSImage;
/// the formatting is covered by formatDimensions tests.
func imageDimensions(path: String) -> String? {
    guard let img = NSImage(contentsOfFile: path),
          let rep = img.representations.first else { return nil }
    return formatDimensions(width: rep.pixelsWide, height: rep.pixelsHigh)
}

/// isMonospacePreferred returns true when a clip's text reads like code or a
/// filesystem path and should render in a monospaced font. Conservative
/// heuristic — favors false for ordinary prose.
func isMonospacePreferred(_ s: String) -> Bool {
    let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
    if t.isEmpty { return false }
    // Filesystem paths.
    if t.hasPrefix("/") || t.hasPrefix("~/") { return true }
    if t.range(of: #"^[A-Za-z]:\\"#, options: .regularExpression) != nil { return true }
    // Code-ish tokens / shell commands.
    let codeMarkers = ["{", "}", ";", "()", "=>", "func ", "def ", "class ",
                       "import ", "const ", "var ", "--", "sudo ", "git ", "::"]
    if codeMarkers.contains(where: { t.contains($0) }) {
        // But an email contains none of these except possibly "--"; guard emails.
        if t.range(of: #"^\S+@\S+\.\S+$"#, options: .regularExpression) != nil { return false }
        return true
    }
    return false
}
