import AppKit
import Carbon.HIToolbox

/// Registers a single global hotkey and invokes a callback on press. The
/// callback runs on the main thread. Hold a reference for the app's lifetime.
///
/// A global hotkey may require Accessibility/Input Monitoring permission the
/// first time it fires; registration itself succeeds without it.
final class GlobalHotkey {
    private var ref: EventHotKeyRef?
    private var handler: EventHandlerRef?
    private let onFire: () -> Void

    init(keyCode: UInt32 = UInt32(kVK_ANSI_V),
         modifiers: UInt32 = UInt32(cmdKey | shiftKey),
         onFire: @escaping () -> Void) {
        self.onFire = onFire
        register(keyCode: keyCode, modifiers: modifiers)
    }

    private func register(keyCode: UInt32, modifiers: UInt32) {
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard),
                                 eventKind: OSType(kEventHotKeyPressed))
        let selfPtr = Unmanaged.passUnretained(self).toOpaque()
        InstallEventHandler(GetApplicationEventTarget(), { _, event, ctx in
            guard let ctx = ctx else { return noErr }
            Unmanaged<GlobalHotkey>.fromOpaque(ctx).takeUnretainedValue().onFire()
            return noErr
        }, 1, &spec, selfPtr, &handler)

        let id = EventHotKeyID(signature: OSType(0x43464E48 /* 'CFNH' */), id: 1)
        RegisterEventHotKey(keyCode, modifiers, id, GetApplicationEventTarget(), 0, &ref)
    }

    deinit {
        if let ref { UnregisterEventHotKey(ref) }
        if let handler { RemoveEventHandler(handler) }
    }
}
