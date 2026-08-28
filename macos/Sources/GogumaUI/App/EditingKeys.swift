import AppKit

/// Makes ⌘V, ⌘C, ⌘X, ⌘A and ⌘Z work in this app's text fields.
///
/// **Why a monitor and not a menu.** An Edit menu is the ordinary answer, and
/// this app has one, correctly built: its Paste item claims `paste:`, validates,
/// and `NSApp.mainMenu.performKeyEquivalent` handles a synthetic ⌘V. It was
/// still not enough, and the self-test says why — with Settings open the app
/// reports `activationPolicy == .regular` but `NSApp.isActive == false` and no
/// key window. A menu bar belongs to the *active* application. An app that never
/// becomes active never has its menu bar consulted, so a perfectly formed Paste
/// item is never reached and ⌘V goes wherever the frontmost app sends it.
///
/// Menu-bar apps land in that state easily: they start as `.accessory`, and
/// `NSApp.activate(ignoringOtherApps:)` is deprecated on macOS 14 and is no
/// longer a guarantee. Rather than fight for activation on every window open —
/// which is what the second attempt at this bug did, and which also risks a Dock
/// icon appearing and vanishing as windows come and go — the keystroke is caught
/// before menu processing and dispatched down the responder chain directly.
///
/// **This cannot eat another app's keys.** `addLocalMonitorForEvents` sees only
/// events already routed to this process; a global monitor is the one that would
/// see everything, and is deliberately not what this is. It also declines unless
/// this app has a key window, so a keystroke arriving with nothing focused is
/// passed through untouched.
///
/// The menu stays. It is how a user discovers the shortcuts exist, it works on
/// any system where the app does become active, and this is the belt to its
/// braces — not a replacement.
@MainActor
enum EditingKeys {

    /// The standard editing messages, by their ⌘ key.
    ///
    /// `z` is undo, which the field editor implements for itself; redo (⇧⌘Z) is
    /// handled by the same selector with the shift flag, so it is matched on the
    /// modifier rather than given a row of its own.
    private static let actions: [String: Selector] = [
        "v": Selector(("paste:")),
        "c": Selector(("copy:")),
        "x": Selector(("cut:")),
        "a": Selector(("selectAll:")),
        "z": Selector(("undo:")),
    ]

    private static var monitor: Any?

    /// Installs the monitor. Idempotent.
    static func install() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { event in
            handle(event) ? nil : event
        }
    }

    /// True when the event was dispatched, and so should not travel further.
    private static func handle(_ event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        // ⌘ exactly, or ⇧⌘ for redo. Anything with ⌥ or ⌃ is somebody else's
        // shortcut and is left alone.
        guard flags == .command || flags == [.command, .shift] else { return false }
        guard let key = event.charactersIgnoringModifiers?.lowercased(),
            let action = actions[key]
        else { return false }

        // Only while one of our windows is focused. With nothing key there is
        // no responder chain worth dispatching into, and passing the event on
        // is the correct, quieter answer.
        guard NSApp.keyWindow != nil else { return false }

        let selector = (key == "z" && flags.contains(.shift)) ? Selector(("redo:")) : action
        return NSApp.sendAction(selector, to: nil, from: nil)
    }
}
