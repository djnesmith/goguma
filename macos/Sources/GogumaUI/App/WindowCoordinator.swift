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

    /// One window, two pages.
    ///
    /// Jobs and Settings are the two things the popover's footer opens, and
    /// they used to be two independent windows: opening one while the other
    /// was up left the user with a second window on top of the first, and
    /// going back and forth accumulated windows they then had to close. They
    /// are pages of the same window now, retargeted the way the history
    /// inspector already was.
    private var mainWindow: NSWindow?
    private var mainHost: NSHostingController<MainWindowView>?
    private var mainPage: MainPage?
    /// The size Jobs was left at, so switching to Settings and back does not
    /// throw away a window the user had sized to their list.
    private var jobsSize: CGSize?
    /// True for a prewarmed window that has never been on screen, so it is
    /// centred on the screen the user is actually looking at when it first is.
    private var needsPlacing = false
    /// Height of the window's title bar, measured once. See `retarget`.
    private var chromeHeight: CGFloat?

    private var historyWindow: NSWindow?
    private var historyHost: NSHostingController<HistoryWindowView>?

    init(store: StatusStore) {
        self.store = store
        super.init()
    }

    // MARK: - Windows

    /// Builds the main window without showing it.
    ///
    /// Constructing the NSWindow and its hosting controller lays out the whole
    /// SwiftUI tree synchronously, which measured 234ms the first time. Paid on
    /// the click, that is the pause between pressing Jobs and seeing Jobs. Paid
    /// shortly after launch, nobody is waiting for it.
    ///
    /// Idempotent, and safe to call when a window already exists.
    func prewarm() {
        guard mainWindow == nil else { return }
        let page = MainPage.jobs
        let host = NSHostingController(
            rootView: MainWindowView(store: store, coordinator: self, page: page)
        )
        host.sizingOptions = .standardBounds
        mainWindow = makeWindow(
            title: page.title,
            size: size(for: page),
            minSize: page.minSize,
            resizable: page.resizable,
            hosting: host
        )
        mainHost = host
        mainPage = page
        // Not yet placed: it was centred on whichever screen was active at
        // launch, and the person may be looking at another one by the time
        // they open it.
        needsPlacing = true
    }

    func showJobs() { show(.jobs) }

    func showSettings() { show(.settings) }

    private func show(_ page: MainPage) {
        let view = MainWindowView(store: store, coordinator: self, page: page)
        guard let mainWindow, let mainHost else {
            let host = NSHostingController(rootView: view)
            host.sizingOptions = .standardBounds
            let window = makeWindow(
                title: page.title,
                size: size(for: page),
                minSize: page.minSize,
                resizable: page.resizable,
                hosting: host
            )
            mainHost = host
            self.mainWindow = window
            mainPage = page
            front(window)
            return
        }
        if needsPlacing {
            needsPlacing = false
            mainWindow.center(on: activeScreen)
        }
        if mainPage != page {
            if mainPage == .jobs {
                jobsSize = CGSize(
                    width: mainWindow.frame.width,
                    height: mainWindow.frame.height - (chromeHeight ?? 0)
                )
            }
            // Shape the window first, then put the new page in it.
            //
            // The other order swapped the content while the frame still
            // belonged to the page being left, so for a frame or two the new
            // pane was drawn into the old window's bounds: Settings appearing
            // inside the Jobs window's outline before it snapped. Resizing an
            // empty window costs nothing; resizing one mid-layout costs a pass.
            retarget(mainWindow, to: page)
            mainHost.rootView = view
        }
        front(mainWindow)
    }

    /// Re-dresses the window for a page: title, resizability, and size.
    ///
    /// The order matters. A minimum size left over from Jobs would refuse the
    /// Settings width, and a resizable mask left on would let the fixed-width
    /// pane be dragged, so both are reset before the frame is set.
    private func retarget(_ window: NSWindow, to page: MainPage) {
        mainPage = page
        window.title = page.title

        var style: NSWindow.StyleMask = [.titled, .closable, .miniaturizable]
        if page.resizable { style.insert(.resizable) }
        window.styleMask = style
        window.contentMinSize = page.minSize ?? .zero

        // Anchored by the top edge and the horizontal centre.
        //
        // Setting the content size alone pins the bottom-left corner, so a
        // page 380pt narrower shrinks away from its right edge and a page
        // 200pt shorter drops its title bar down the screen. Both leave the
        // window sitting somewhere the user did not put it: switching from
        // Jobs to Settings left the smaller pane hanging off to the left of
        // where the window had been. Holding the centre and the top means the
        // window changes size around the place it already occupied.
        var target = size(for: page)
        var frame = window.frame
        if page.sizesItsOwnHeight {
            // Keep what is there; the pane corrects it after it measures.
            target.height = frame.height - (chromeHeight ?? 0)
        }
        // The title bar's height, measured once.
        //
        // `contentLayoutRect` is the whole cost of switching page: reading it
        // forces AppKit to lay out the SwiftUI tree that was just installed,
        // synchronously, before this function can continue. It measured 380ms
        // on the Settings pane, every single time. The chrome is a constant
        // for a given style mask, so it is worth measuring once and never
        // asking again.
        let chrome = chromeHeight ?? {
            let h = frame.height - window.contentLayoutRect.height
            chromeHeight = h
            return h
        }()
        let midX = frame.midX
        frame.origin.y += frame.height - (target.height + chrome)
        frame.size = NSSize(width: target.width, height: target.height + chrome)
        frame.origin.x = midX - target.width / 2
        // `display: false`.
        //
        // Passing true makes AppKit lay out and redraw the hosted SwiftUI
        // content synchronously inside setFrame, which measured 189ms on the
        // Jobs to Settings switch: the whole cost of changing page was one
        // argument. The window is redrawn on the next display cycle either
        // way, and the frame is set before it is ordered front, so nothing is
        // ever seen at the old size.
        window.setFrame(frame, display: false, animate: false)
        // Back onto the screen if centring pushed it off the edge, which it
        // can when the window was already up against one.
        if let visible = window.screen?.visibleFrame, !visible.contains(frame) {
            window.setFrame(frame.nudged(into: visible), display: false, animate: false)
        }
    }

    private func size(for page: MainPage) -> CGSize {
        switch page {
        case .jobs: jobsSize ?? Theme.Surface.jobsWindowSize
        case .settings:
            // Opened at the height this tab measured last time, so there is no
            // band of empty surface for the frame or two before the pane
            // corrects it. The constant is only ever the first-ever open.
            CGSize(
                width: Theme.Surface.settingsWindowSize.width,
                height: settingsHeightForCurrentTab()
            )
        }
    }

    /// The height to open Settings at: what its current tab measured last time,
    /// or the constant if it has never been opened.
    private func settingsHeightForCurrentTab() -> CGFloat {
        let tab = UserDefaults.standard.string(forKey: "settings.tab") ?? "timing"
        let stored = UserDefaults.standard.double(forKey: "settings.height.\(tab)")
        // A stored zero means never measured, and anything implausible is not
        // worth opening a window at.
        return stored > 120 ? CGFloat(stored) : Theme.Surface.settingsWindowSize.height
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
        if closing === mainWindow {
            if mainPage == .jobs {
                jobsSize = CGSize(
                    width: closing.frame.width,
                    height: closing.frame.height - (chromeHeight ?? 0)
                )
            }
            mainWindow = nil
            mainHost = nil
            mainPage = nil
        }
        if closing === historyWindow {
            historyWindow = nil
            historyHost = nil
        }
        // Drop back to a menu-bar-only app once the last window is gone, so
        // goguma doesn't sit in the Dock doing nothing.
        if mainWindow == nil, historyWindow == nil {
            NSApp.setActivationPolicy(.accessory)
        }
    }

    /// True when any real window is open. The delegate uses this to decide
    /// whether a re-open needs to present something.
    var hasOpenWindow: Bool {
        mainWindow != nil || historyWindow != nil
    }
}

/// Which page the main window is showing.
enum MainPage {
    case jobs
    case settings

    var title: String {
        switch self {
        case .jobs: "goguma Jobs"
        case .settings: "goguma Settings"
        }
    }

    /// Settings is a fixed-width pane that sizes its own height to its content
    /// (see `FitsWindowHeight`), so letting it be dragged only ever produces a
    /// column of empty surface.
    var resizable: Bool {
        switch self {
        case .jobs: true
        case .settings: false
        }
    }

    var minSize: CGSize? {
        switch self {
        case .jobs: Theme.Surface.jobsWindowMinSize
        case .settings: nil
        }
    }

    /// Whether the page sets its own height once it has measured its content.
    ///
    /// Settings does, through `FitsWindowHeight`. Setting a height here as
    /// well means the window is laid out twice on every switch: once at the
    /// guessed height, and again at the real one a moment later. Each pass is
    /// a full SwiftUI layout of the pane, and that pane is the expensive one.
    var sizesItsOwnHeight: Bool {
        switch self {
        case .jobs: false
        // Back to measuring, now that measuring is cheap.
        //
        // A fixed height has to fit the tallest tab, which left every shorter
        // one with a band of dead surface under the status bar: about 100pt of
        // nothing below "Connected" on the tab the pane opens on. The double
        // layout this describes was expensive when the pane was 1266pt of every
        // setting at once. A tab is a few hundred points and measures in
        // nothing, so the pane can fit its content again and each tab gets the
        // window it actually needs.
        case .settings: true
        }
    }
}

/// The main window's content, which is whichever page is showing.
struct MainWindowView: View {
    let store: StatusStore
    let coordinator: WindowCoordinator
    let page: MainPage

    var body: some View {
        switch page {
        case .jobs:
            JobsWindowView(store: store, coordinator: coordinator)
        case .settings:
            SettingsWindowView(store: store)
        }
    }
}

private extension NSRect {
    /// Slides a frame back inside `bounds` without resizing it.
    func nudged(into bounds: NSRect) -> NSRect {
        var r = self
        r.origin.x = Swift.min(Swift.max(r.origin.x, bounds.minX), bounds.maxX - r.width)
        r.origin.y = Swift.min(Swift.max(r.origin.y, bounds.minY), bounds.maxY - r.height)
        return r
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
