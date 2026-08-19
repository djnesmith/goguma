import AppKit
import SwiftUI

/// Application shell.
///
/// Deliberately thin: it wires the store to the status item, installs a minimal
/// main menu, and gets out of the way. goguma's app does no privileged work
/// and holds no state of its own; everything lives in the daemon, and this is
/// a viewer with buttons.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var store: StatusStore { AppEnvironment.store }
    private var coordinator: WindowCoordinator { AppEnvironment.coordinator }

    /// Held for the process lifetime: releasing it removes the menu bar item.
    private var menuBar: FluidMenuBarExtraStatusItem?

    func applicationDidFinishLaunching(_: Notification) {
        // Menu bar app: no Dock icon, no app switcher entry, until a real
        // window opens (see WindowCoordinator).
        NSApp.setActivationPolicy(.accessory)
        NSApp.mainMenu = MainMenu.build()

        installMenuBarItem()
        store.startPolling()
        observeWake()

        // Build the main window now, so the first click on Jobs or Settings
        // does not pay for it. See WindowCoordinator.prewarm.
        let warm = coordinator
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { warm.prewarm() }

        // One-off config read at launch. Settings is the only surface that
        // edits config, but the popover needs `low_battery_cutout_pct` to know
        // when to tint the battery reading as a problem, and waiting for the
        // user to open Settings first would leave that quietly wrong.
        Task { await store.loadConfig() }

        presentFirstRunIfNeeded()
    }

    /// Opens the popover once, on the first launch that finds no daemon.
    ///
    /// This is an `LSUIElement` app: no Dock icon, no window, no app switcher
    /// entry. Double-clicking it in Applications therefore produced no visible
    /// response at all beyond a new glyph appearing among a row of menu bar
    /// icons. Someone who has just dragged an app across and opened it is
    /// entitled to think it failed, and the one thing they need to do next was
    /// behind a click they had no reason to make.
    ///
    /// Only when there is genuinely something to do. A launch that finds a
    /// working daemon opens nothing, because a menu bar app that presents
    /// itself unbidden every login is a worse offence than a quiet one.
    private func presentFirstRunIfNeeded() {
        guard Onboarding.canSelfInstall, !Onboarding.hasPresentedFirstRun else { return }

        // After the first poll, not immediately: the store starts in
        // `.connecting`, so asking now reports "not running" on every launch
        // including the ones where it is running perfectly well.
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) { [weak self] in
            guard let self, self.store.connection.blocksContent else { return }
            Onboarding.hasPresentedFirstRun = true
            self.menuBar?.showWindow()
        }
    }

    /// Polls the moment the machine wakes, rather than whenever the timer next
    /// comes round.
    ///
    /// Idle polling is thirty seconds apart, so opening the lid showed whatever
    /// was true before it closed, for up to half a minute. Combined with the
    /// first socket attempt after a wake tending to fail, that is how a service
    /// which had been running throughout came to be reported as not running at
    /// all. The wake notification is the moment the answer changes, so it is
    /// the moment to ask.
    private func observeWake() {
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            guard let self else { return }
            Task { @MainActor in
                // A moment first: the socket, the daemon's own poll and the
                // network all come back over the second or so after a wake, and
                // asking into the middle of that is what produced the failure
                // being recovered from.
                try? await Task.sleep(for: .milliseconds(800))
                await self.store.refresh()
            }
        }
    }

    private func installMenuBarItem() {
        let window = FluidMenuBarExtraWindow(title: "goguma") {
            PopoverView(store: AppEnvironment.store, coordinator: AppEnvironment.coordinator)
        }
        menuBar = FluidMenuBarExtraStatusItem(
            title: "goguma",
            image: .direct(MenuBarIcon.image(for: store.state)),
            window: window,
            // Under the item and growing down-right, like every other menu.
            alignment: .left
        )
        trackIcon()
    }

    /// Keeps the menu bar glyph in step with the daemon.
    ///
    /// `withObservationTracking` fires once and then stops, so the callback
    /// re-arms it. Without the re-arm the icon updates exactly one time and
    /// then freezes on whatever state happened to be second, which looks
    /// identical to the daemon having stopped reporting.
    private func trackIcon() {
        withObservationTracking {
            menuBar?.button?.image = MenuBarIcon.image(for: store.state)
        } onChange: { [weak self] in
            Task { @MainActor in self?.trackIcon() }
        }
    }

    func applicationWillTerminate(_: Notification) {
        // Leave the daemon alone. Unlike some menu bar apps, quitting goguma
        // must NOT pause it: the daemon exists to wake the machine for cron jobs
        // whether or not anyone is looking at a UI, and silently disabling that
        // on quit would lose the user's jobs.
        store.stopPolling()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_: NSApplication) -> Bool {
        false
    }

    /// Re-launching from Finder or Spotlight while already running. With no
    /// window open there is nothing to front, so show the Jobs window rather
    /// than appearing to do nothing.
    func applicationShouldHandleReopen(_: NSApplication, hasVisibleWindows: Bool) -> Bool {
        if hasVisibleWindows {
            NSApp.activate(ignoringOtherApps: true)
        } else {
            coordinator.showJobs()
        }
        return true
    }
}

/// The application main menu.
///
/// An accessory app has no menu bar until it becomes `.regular`, but the Edit
/// menu still has to exist before that happens: without it, ⌘X/⌘C/⌘V/⌘A do
/// nothing in the text fields of the job editor, which reads as a broken app.
enum MainMenu {
    static func build() -> NSMenu {
        let main = NSMenu()

        let appItem = NSMenuItem()
        let appMenu = NSMenu()
        appMenu.addItem(
            withTitle: "About goguma",
            action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)),
            keyEquivalent: ""
        )
        appMenu.addItem(.separator())
        appMenu.addItem(
            withTitle: "Hide goguma",
            action: #selector(NSApplication.hide(_:)),
            keyEquivalent: "h"
        )
        appMenu.addItem(.separator())
        appMenu.addItem(
            withTitle: "Quit goguma",
            action: #selector(NSApplication.terminate(_:)),
            keyEquivalent: "q"
        )
        appItem.submenu = appMenu
        main.addItem(appItem)

        let editItem = NSMenuItem()
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(
            withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x"
        )
        editMenu.addItem(
            withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c"
        )
        editMenu.addItem(
            withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v"
        )
        editMenu.addItem(
            withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a"
        )
        editItem.submenu = editMenu
        main.addItem(editItem)

        let windowItem = NSMenuItem()
        let windowMenu = NSMenu(title: "Window")
        windowMenu.addItem(
            withTitle: "Close", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w"
        )
        windowMenu.addItem(
            withTitle: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)),
            keyEquivalent: "m"
        )
        windowItem.submenu = windowMenu
        main.addItem(windowItem)

        return main
    }
}
