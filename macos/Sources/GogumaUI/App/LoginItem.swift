import AppKit
import ServiceManagement

/// Whether the goguma **app** opens when the user logs in.
///
/// This is the GUI app and nothing else. The daemon that runs the jobs has its
/// own launch agent at `~/Library/LaunchAgents/glass.goguma.daemon.plist` with
/// `RunAtLoad`, installed by setup, and none of this touches it. Someone who
/// turns this off still has their jobs run; they just do not get the menu bar
/// icon until they open the app.
///
/// **Deliberately stateless.** There is no stored flag, and adding one would be
/// a bug rather than a cache. `SMAppService` already holds the answer, and a
/// second copy of it goes stale in every direction that matters: a
/// `register()` that threw, a user who switched the item off in System
/// Settings, a copy of the app that was moved or thrown away. Each of those
/// leaves a remembered `true` showing "on" beside an app that will not start.
/// Every read here asks the system.
@MainActor
enum LoginItem {
    /// What macOS says right now.
    ///
    /// Only `.enabled` means the app will open at the next login, and callers
    /// are expected to treat everything else as off. `.requiresApproval` is
    /// the one that tempts otherwise: a registration exists, but it is
    /// switched off in System Settings, which is the one place it can be
    /// switched back on. `register()` cannot override that and returns success
    /// without changing anything, so counting it as on would be exactly the
    /// silent lie this type is shaped to avoid.
    static var status: SMAppService.Status { SMAppService.mainApp.status }

    /// Turn it on or off, reporting failure rather than swallowing it.
    ///
    /// `unregister()` throws when there is nothing registered, which is a
    /// no-op being reported as an error: turning off something already off is
    /// the state the caller asked for. `register()` is left to throw.
    static func setEnabled(_ enabled: Bool) throws {
        guard enabled else {
            if status != .notRegistered && status != .notFound {
                try SMAppService.mainApp.unregister()
            }
            return
        }
        try SMAppService.mainApp.register()
    }

    /// Where macOS records the app when it is registered from here.
    ///
    /// `register()` takes the running bundle's path, and the login item keeps
    /// it. Verified by registering a probe bundle and reading the entry back:
    /// System Events named the exact directory the probe was run from, not a
    /// canonical location. So a copy opened from ~/Downloads or from a build
    /// directory registers that path, and the login item breaks silently the
    /// moment that copy moves or is deleted.
    static var bundlePath: String { Bundle.main.bundlePath }

    /// Whether this copy sits somewhere a login item can keep pointing at.
    ///
    /// Both Applications folders count: macOS treats `~/Applications` as a
    /// real install location, and someone without admin rights has nowhere
    /// else to put an app.
    static var isInstalled: Bool {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return bundlePath.hasPrefix("/Applications/")
            || bundlePath.hasPrefix("\(home)/Applications/")
    }

    /// Open the pane where the user can undo a `.requiresApproval`.
    static func openSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }
}
