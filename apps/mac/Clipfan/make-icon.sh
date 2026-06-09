#!/usr/bin/env bash
# Renders the clipfan app icon from the same fan-card artwork used by the menu
# bar icon, then packs all required sizes into Resources/AppIcon.icns.
#
# Must run in a logged-in GUI session: the master is drawn with NSImage.lockFocus,
# which needs a window-server connection (it can fail on headless CI runners). The
# generated .icns is committed, so normal builds never regenerate it.
set -euo pipefail
cd "$(dirname "$0")"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
iconset="$work/AppIcon.iconset"
mkdir -p "$iconset" Resources

# Draw a 1024px master with the shared Swift artwork, then downscale with sips.
cat > "$work/draw.swift" <<'SWIFT'
import AppKit

@main
struct DrawClipfanAppIcon {
    static func main() throws {
        let image = ClipfanMenuBarIconArtwork.appIconImage(size: 1024)
        let rep = NSBitmapImageRep(data: image.tiffRepresentation!)!
        let png = rep.representation(using: .png, properties: [:])!
        try png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
    }
}
SWIFT

master="$work/master.png"
swiftc "$work/draw.swift" Sources/Clipfan/MenuBarIcon.swift -o "$work/draw-icon"
"$work/draw-icon" "$master"

gen() { sips -z "$2" "$2" "$master" --out "$iconset/icon_$1.png" >/dev/null; }
gen 16x16        16
gen 16x16@2x     32
gen 32x32        32
gen 32x32@2x     64
gen 128x128      128
gen 128x128@2x   256
gen 256x256      256
gen 256x256@2x   512
gen 512x512      512
gen 512x512@2x   1024

iconutil -c icns "$iconset" -o Resources/AppIcon.icns
echo "Wrote Resources/AppIcon.icns"
