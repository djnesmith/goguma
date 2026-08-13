import AppKit
import SwiftUI

/// Owns the app's real windows.
///
/// goguma is an `LSUIElement`-style accessory app, so SwiftUI's `Scene`
/// lifecycle never presents anything on its own. Windows are created here with
/// AppKit and hosted with `NSHostingController`, which also lets the activation
/// policy follow window visibility: accessory (menu bar only) with nothing
/// open, regular (app menu, ⌘Q, Dock icon) while a window is up.
@MainActor
final class WindowCoordinator: NSObject, NSWindowDelegate {
    private let store: StatusStore

    private var jobsWindow: NSWindow?
    private var settingsWindow: NSWindow?
    private var historyWindow: NSWindow?
    private var historyHost: NSHostingController<HistoryWindowView>?

    init(store: StatusStore) {
        self.store = store
        super.init()
    }

    // MARK: - Windows

    func showJobs() {
        if let jobsWindow {
            front(jobsWindow)
            return
        }
        let window = makeWindow(
            title: "goguma Jobs",
            size: Theme.Surface.jobsWindowSize,
            minSize: Theme.Surface.jobsWindowMinSize,
            content: JobsWindowView(store: store, coordinator: self)
        )
        jobsWindow = window
        front(window)
    }

    func showSettings() {
        if let settingsWindow {
            front(settingsWindow)
            return
        }
        let window = makeWindow(
            title: "goguma Settings",
            size: Theme.Surface.settingsWindowSize,
            minSize: nil,
            resizable: false,
            content: SettingsWindowView(store: store)
        )
        settingsWindow = window
        front(window)
    }

    /// One history window, retargeted rather than duplicated: opening history
    /// for a second job replaces the contents of the existing window, which is
    /// what a single-selection inspector should do.
    func showHistory(forJobID jobID: String) {
        let view = HistoryWindowView(store: store, jobID: jobID)
        if let historyWindow, let historyHost {
            historyHost.rootView = view
            historyWindow.title = historyTitle(for: jobID)
            front(historyWindow)
            return
        }
        let host = NSHostingController(rootView: view)
        host.sizingOptions = .standardBounds
        let window = makeWindow(
            title: historyTitle(for: jobID),
            size: Theme.Surface.historyWindowSize,
            minSize: Theme.Surface.historyWindowMinSize,
            hosting: host
        )
        historyHost = host
        historyWindow = window
        front(window)
    }

    private func historyTitle(for jobID: String) -> String {
        let name = store.job(withID: jobID)?.job.displayName ?? jobID
        return "History · \(name)"
    }

    func quit() {
        NSApp.terminate(nil)
    }

    // MARK: - Construction

    private func makeWindow(
        title: String,
        size: CGSize,
        minSize: CGSize?,
        resizable: Bool = true,
        content: some View
    ) -> NSWindow {
        let host = NSHostingController(rootView: content)
        // `.standardBounds` keeps SwiftUI's min/ideal/max sizing without routing
        // ideal-size changes through `preferredContentSize`, which AppKit
        // updates during a constraints pass and which can recurse.
        host.sizingOptions = .standardBounds
        return makeWindow(
            title: title, size: size, minSize: minSize, resizable: resizable, hosting: host
        )
    }

    private func makeWindow(
        title: String,
        size: CGSize,
        minSize: CGSize?,
        resizable: Bool = true,
        hosting: NSViewController
    ) -> NSWindow {
        let window = NSWindow(contentViewController: hosting)
        window.title = title
        var style: NSWindow.StyleMask = [.titled, .closable, .miniaturizable]
        if resizable { style.insert(.resizable) }
        window.styleMask = style
        window.setContentSize(NSSize(width: size.width, height: size.height))
        if let minSize {
            window.contentMinSize = NSSize(width: minSize.width, height: minSize.height)
        }

        // Follow the user to whatever Space they are on, rather than dragging
        // them to wherever this window happens to live.
        //
        // A menu bar app is reachable from every Space, so its windows have to
        // be too. Without this, opening Jobs while a full-screen app is focused
        // makes macOS animate the whole desktop sideways to the Space where the
        // window was first created: the window arrives, but the user has been
        // thrown out of what they were doing to get to it. `.moveToActiveSpace`
        // brings the window to them instead. `.fullScreenAuxiliary` lets it
        // appear *over* a full-screen app rather than forcing a Space switch to
        // escape one.
        window.collectionBehavior = [.moveToActiveSpace, .fullScreenAuxiliary]
        window.center(on: activeScreen)
        // ARC owns these (see the stored properties). A self-releasing window
        // over-releases at the next autorelease-pool drain and crashes.
        window.isReleasedWhenClosed = false
        window.delegate = self
        return window
    }

    private func front(_ window: NSWindow) {
        NSApp.setActivationPolicy(.regular)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        // Nothing starts focused.
        //
        // AppKit hands first responder to the first text field it finds, which
        // put a blinking caret in the Jobs filter every time the window opened.
        // A caret is a promise that typing goes somewhere, and someone opening
        // this window is far more likely to be reading it than filtering it.
        // Clicking the field still focuses it normally.
        window.makeFirstResponder(nil)
    }

    /// The screen the user is actually looking at.
    ///
    /// `NSScreen.main` is the screen with the *key window*, which for a menu bar
    /// app is usually whatever app they were using, but it is `nil` or stale
    /// often enough to matter on a multi-display desk. The pointer is the more
    /// reliable signal for "where is the person", since they just clicked the
    /// menu bar with it.
    private var activeScreen: NSScreen? {
        let mouse = NSEvent.mouseLocation
        return NSScreen.screens.first { NSMouseInRect(mouse, $0.frame, false) }
            ?? NSApp.keyWindow?.screen
            ?? NSScreen.main
    }

    // MARK: - NSWindowDelegate

    func windowWillClose(_ notification: Notification) {
        guard let closing = notification.object as? NSWindow else { return }
        if closing === jobsWindow { jobsWindow = nil }
        if closing === settingsWindow { settingsWindow = nil }
        if closing === historyWindow {
            historyWindow = nil
            historyHost = nil
        }
        // Drop back to a menu-bar-only app once the last window is gone, so
        // goguma doesn't sit in the Dock doing nothing.
        if jobsWindow == nil, settingsWindow == nil, historyWindow == nil {
            NSApp.setActivationPolicy(.accessory)
        }
    }

    /// True when any real window is open. The delegate uses this to decide
    /// whether a re-open needs to present something.
    var hasOpenWindow: Bool {
        jobsWindow != nil || settingsWindow != nil || historyWindow != nil
    }
}

private extension NSWindow {
    /// Centres on a specific screen rather than the system's idea of the main
    /// one, which is what `center()` uses and which is how a window ends up on
    /// a display the user is not looking at.
    func center(on screen: NSScreen?) {
        guard let screen else {
            center()
            return
        }
        // Biased above true centre, matching how macOS places its own windows:
        // a geometrically centred window reads as sitting slightly low, because
        // the eye weights the top of a frame more heavily than the bottom.
        let visible = screen.visibleFrame
        setFrameOrigin(
            NSPoint(
                x: visible.midX - frame.width / 2,
                y: visible.midY - frame.height / 2 + visible.height * 0.08
            )
        )
    }
}
