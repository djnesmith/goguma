import AppKit
import SwiftUI

/// A debug-only harness for the one thing that cannot be checked by reading:
/// whether ⌘V actually reaches a text field in this app.
///
/// It exists because two plausible fixes were shipped for a paste bug and
/// neither worked, and both were reasoned rather than measured. A menu can be
/// well-formed, its selectors correct, and its owning app still never receive
/// the keystroke — so the only useful question is what the running app reports
/// about itself with a real window on screen.
///
/// Run with `GogumaUI --selftest-paste`. It opens Settings, prints what the
/// responder chain and menu bar actually look like, injects a ⌘V by the same
/// path a keystroke takes, and says whether the field's text changed.
///
/// Not compiled out by a flag, because it is reached only by an argument the
/// app is never launched with. It touches nothing unless asked.
@MainActor
enum PasteSelfTest {

    static func runIfRequested(coordinator: WindowCoordinator) -> Bool {
        guard CommandLine.arguments.contains("--selftest-paste") else { return false }
        // After launch has settled: the window is prewarmed asynchronously and
        // activation is not instant.
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) { run(coordinator: coordinator) }
        return true
    }

    private static func run(coordinator: WindowCoordinator) {
        var failures = 0
        var report = "=== goguma paste self-test ===\n\n"
        func check(_ label: String, _ ok: Bool, _ detail: String = "") {
            let line = "  \(ok ? "PASS" : "FAIL")  \(label)\(detail.isEmpty ? "" : ": \(detail)")"
            print(line)
            report += line + "\n"
            if !ok { failures += 1 }
        }
        // Written to a file as well as stdout, because the faithful way to run
        // this is `open -a`, which launches through Launch Services — the only
        // way the app activates and takes a key window the way it does for a
        // user — and swallows stdout entirely.
        func flush() {
            let out = ProcessInfo.processInfo.environment["GOGUMA_SELFTEST_OUT"]
                ?? NSTemporaryDirectory() + "goguma-selftest.txt"
            try? report.write(toFile: out, atomically: true, encoding: .utf8)
        }

        print("\n=== goguma paste self-test ===\n")

        coordinator.showSettings()
        // Let the window become key and the activation policy settle.
        RunLoop.current.run(until: Date().addingTimeInterval(1.0))

        // 1. What is the app, right now, with a real window up?
        let policy = NSApp.activationPolicy()
        let policyName: String
        switch policy {
        case .regular: policyName = "regular"
        case .accessory: policyName = "accessory"
        case .prohibited: policyName = "prohibited"
        @unknown default: policyName = "unknown"
        }
        check("activation policy is .regular", policy == .regular, policyName)
        check("app is active", NSApp.isActive)

        // 2. Is there a menu bar at all, and does it carry paste:?
        let main = NSApp.mainMenu
        check("mainMenu exists", main != nil)
        let editItem = main?.items.first { $0.submenu?.title == "Edit" || $0.title == "Edit" }
        check("an Edit menu is installed", editItem != nil)
        let pasteItem = editItem?.submenu?.items.first { $0.action == Selector(("paste:")) }
        check("Edit carries paste:", pasteItem != nil,
            pasteItem.map { "key=\($0.keyEquivalent) mods=\($0.keyEquivalentModifierMask.rawValue)" } ?? "")

        // 3. Is there a key window and can it hold a first responder?
        let key = NSApp.keyWindow
        check("a key window exists", key != nil, key?.title ?? "none")
        check("key window canBecomeKey", key?.canBecomeKey ?? false)

        // 4. Put a caret in a text field, the way a click would.
        //
        // selectKeyView walks to the first field rather than guessing at the
        // view tree, which is what a Tab press does.
        key?.selectNextKeyView(nil)
        RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        let responder = key?.firstResponder
        let responderName = responder.map { String(describing: type(of: $0)) } ?? "none"
        check("something holds first responder", responder != nil, responderName)
        let respondsToPaste = responder?.responds(to: Selector(("paste:"))) ?? false
        check("first responder answers paste:", respondsToPaste, responderName)

        // 5. Would the menu item validate right now? This is what greys it out.
        if let pasteItem {
            let valid = NSApp.validateMenuItem(pasteItem)
            check("NSApp validates the Paste item", valid)
        }

        // 6. The real question: does a ⌘V key equivalent get handled?
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString("PASTED-BY-SELFTEST", forType: .string)

        let route = sendCommandV(to: key)
        check("⌘V was handled", route != .unhandled, route.rawValue)
        // Which path took it matters more than whether one did. Only the menu
        // path is what a real keystroke uses; sendAction succeeding on its own
        // just means some responder somewhere would accept the message.
        check("⌘V was taken by the MENU key-equivalent path (what a real "
            + "keystroke uses)", route == .menu, route.rawValue)

        // 6b. The mouse path. A menu bar app's user reaches for right-click
        // when a shortcut fails, so whether the field editor offers Paste is
        // worth knowing before adding a custom menu that would replace it.
        let contextItems = contextMenuItems(in: key)
        check("a text field offers a context menu", !contextItems.isEmpty,
            contextItems.isEmpty ? "none found" : "\(contextItems.count) items")
        check("that context menu offers Paste",
            contextItems.contains { $0.localizedCaseInsensitiveContains("paste") },
            contextItems.prefix(8).joined(separator: ", "))

        // 7. And did anything actually receive it?
        RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        let landed = fieldContents(in: key).contains { $0.contains("PASTED-BY-SELFTEST") }
        check("the pasteboard string landed in a text field", landed)

        let verdict = "\n  \(failures == 0 ? "ALL PASS" : "\(failures) FAILURE(S)")\n"
        print(verdict)
        report += verdict
        flush()
        exit(failures == 0 ? 0 : 1)
    }

    /// Sends ⌘V the way the system does: as a key-equivalent event offered to
    /// the menu first, then to the window.
    enum Route: String {
        case menu = "handled by NSApp.mainMenu.performKeyEquivalent"
        case window = "handled by window.performKeyEquivalent"
        case sendAction = "handled only by NSApp.sendAction (no key path)"
        case unhandled = "nothing handled it"
    }

    private static func sendCommandV(to window: NSWindow?) -> Route {
        guard
            let event = NSEvent.keyEvent(
                with: .keyDown, location: .zero, modifierFlags: .command,
                timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: window?.windowNumber ?? 0,
                context: nil, characters: "v", charactersIgnoringModifiers: "v",
                isARepeat: false, keyCode: 9)
        else { return .unhandled }

        // The menu gets first refusal — this is the step a real keystroke takes.
        if NSApp.mainMenu?.performKeyEquivalent(with: event) == true { return .menu }
        // Then the window's own chain.
        if window?.performKeyEquivalent(with: event) == true { return .window }
        // Last, the bare action dispatch a fallback monitor would use. Passing
        // here and failing above is the signature of the bug: a responder that
        // would accept paste:, and no route that delivers it.
        if NSApp.sendAction(Selector(("paste:")), to: nil, from: nil) { return .sendAction }
        return .unhandled
    }

    /// The standard context menu a text field offers, if any. Empty when there
    /// is no field, or the field supplies no menu.
    private static func contextMenuItems(in window: NSWindow?) -> [String] {
        guard let root = window?.contentView else { return [] }
        var found: NSMenu?
        func walk(_ v: NSView) {
            if found != nil { return }
            if let field = v as? NSTextField {
                // The field editor is what actually owns the editing menu; fall
                // back to the field's own menu when it is not editing.
                let editor = field.currentEditor() as? NSTextView
                found = editor?.menu ?? field.menu
            }
            v.subviews.forEach(walk)
        }
        walk(root)
        return found?.items.map(\.title) ?? []
    }

    /// Every text value currently in the window, so the test can tell whether
    /// the paste landed without knowing which field it went to.
    private static func fieldContents(in window: NSWindow?) -> [String] {
        guard let root = window?.contentView else { return [] }
        var out: [String] = []
        func walk(_ v: NSView) {
            if let field = v as? NSTextField { out.append(field.stringValue) }
            if let text = v as? NSText { out.append(text.string) }
            v.subviews.forEach(walk)
        }
        walk(root)
        return out
    }
}
