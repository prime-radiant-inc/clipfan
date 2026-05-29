// clipfan-pasteboard-helper — writes a multi-type NSPasteboard item with
// both an image (public.png) and a string (public.utf8-plain-text). Built
// at install time by `dist/install.sh` on macOS using the Xcode CLT swiftc.
//
// Usage: clipfan-pasteboard-helper <image-path>
//   The image at <image-path> is loaded; the same path string is also
//   set as the text representation. Single pasteboard item, two types.

import AppKit
import Foundation

let args = CommandLine.arguments

// --check-concealed: inspect the pasteboard's declared types and print
// "concealed" when the current item is marked secret or transient (e.g. by a
// password manager via org.nspasteboard.ConcealedType). pbpaste and osascript
// cannot surface these custom UTIs, so the daemon shells out to this mode to
// decide whether a clip should be excluded from history.
if args.contains("--check-concealed") {
    let types = NSPasteboard.general.types ?? []
    let markers: Set<NSPasteboard.PasteboardType> = [
        NSPasteboard.PasteboardType("org.nspasteboard.ConcealedType"),
        NSPasteboard.PasteboardType("org.nspasteboard.TransientType"),
    ]
    if !markers.isDisjoint(with: Set(types)) {
        print("concealed")
    }
    exit(0)
}

if args.count != 2 {
    FileHandle.standardError.write("usage: \(args[0]) <image-path>\n".data(using: .utf8)!)
    exit(2)
}
let path = args[1]
guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else {
    FileHandle.standardError.write("could not read \(path)\n".data(using: .utf8)!)
    exit(1)
}

let pb = NSPasteboard.general
pb.clearContents()
let item = NSPasteboardItem()
item.setData(data, forType: NSPasteboard.PasteboardType("public.png"))
item.setString(path, forType: .string)
pb.writeObjects([item])
