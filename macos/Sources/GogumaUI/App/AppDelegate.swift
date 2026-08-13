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

        // One-off config read at launch. Settings is the only surface that
        // edits config, but the popover needs `low_battery_cutout_pct` to know
        // when to tint the battery reading as a problem, and waiting for the
        // user to open Settings first would leave that quietly wrong.
        Task { await store.loadConfig() }
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
