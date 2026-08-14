import AppKit
import SwiftUI

/// The application.
///
/// **The menu bar item is a vendored `FluidMenuBarExtra`, not SwiftUI's
/// `MenuBarExtra`.** That is not a stylistic preference; it is the fix for a
/// bug that took most of a day.
///
/// This surface changes height: the job list expands and collapses. SwiftUI's
/// `MenuBarExtra` resizes by keeping its *bottom* edge fixed, so expanding
/// pushed the top of the window upward, past the menu bar, and cut the header
/// off. There is no API to change that. The package's status item recomputes
/// the frame from the status item's own position on every content size change
/// (`setWindowFrame`), pinning the *top* under the menu bar and growing
/// downward, which is what a menu does.
///
/// It also supplies the things the hand-rolled `NSPanel` attempt got wrong:
/// `.nonactivatingPanel` at `.statusBar` level so the window stays open without
/// stealing activation, and local/global event monitors for click-outside
/// dismissal.
///
/// The status item is built in `AppDelegate` rather than through the package's
/// `Scene` wrapper, because that wrapper sets the button image once at init and
/// goguma's glyph tracks daemon state.
@main
struct GogumaApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    /// Development tooling: `--render <surface> <path> [light|dark]` writes a
    /// PNG and exits without showing anything. See `SurfaceRenderer` for why it
    /// exists. Handled before the scene so nothing is presented.
    init() {
        guard let flag = CommandLine.arguments.firstIndex(of: "--render"),
            CommandLine.arguments.count > flag + 2
        else { return }
        let surface = CommandLine.arguments[flag + 1]
        let path = CommandLine.arguments[flag + 2]
        let dark = CommandLine.arguments.count > flag + 3
            && CommandLine.arguments[flag + 3] == "dark"

        let done = DispatchSemaphore(value: 0)
        Task { @MainActor in
            await SurfaceRenderer.run(surface: surface, path: path, dark: dark)
            done.signal()
        }
        while done.wait(timeout: .now()) == .timedOut {
            RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
        }
        exit(0)
    }

    /// The status item is the app's real surface and it lives in the delegate,
    /// so this scene exists only because `App` requires one. `Settings` is the
    /// same placeholder the package's own wrapper uses; goguma installs its
    /// own main menu in `AppDelegate`, so nothing is presented from here.
    var body: some Scene {
        Settings {}
    }
}

/// The dropdown's contents.
private struct MenuBarRoot: View {
    var body: some View {
        PopoverView(store: AppEnvironment.store, coordinator: AppEnvironment.coordinator)
    }
}

/// The bear, in the state the daemon reports.
///
/// An `NSImage` rather than a SwiftUI view: the status item is an
/// `NSStatusItem`, so the glyph is applied to its button. The tint is baked in
/// here rather than left to `contentTintColor`, which does not apply to
/// non-template images and silently did nothing.
enum MenuBarIcon {
    @MainActor
    static func image(for state: GogumaState) -> NSImage {
        let base: NSImage
        switch state {
        case .idle:
            base = Theme.Colors.sweetPotato
                ? SweetPotatoMark.image(size: Theme.StatusItem.glyphSize, asleep: true)
                : MenuBarMark.image(size: Theme.StatusItem.glyphSize, asleep: true)
        case .holding:
            base = Theme.Colors.sweetPotato
                ? SweetPotatoMark.image(size: Theme.StatusItem.glyphSize, asleep: false)
                : MenuBarMark.image(size: Theme.StatusItem.glyphSize, asleep: false)
        // The mark, in every state.
        //
        // These three used to swap it for an SF Symbol, so a Mac whose daemon
        // was not running showed a stock system glyph where the app's own mark
        // belongs, and the icon a user looks for to find goguma was the one
        // thing missing exactly when they needed to open it. The menu bar says
        // *which app*, not *what it is doing*; the popover behind it says the
        // rest, in words, with colour.
        case .paused, .cutout, .disconnected:
            base = Theme.Colors.sweetPotato
                ? SweetPotatoMark.image(size: Theme.StatusItem.glyphSize, asleep: true)
                : MenuBarMark.image(size: Theme.StatusItem.glyphSize, asleep: true)
        }

        // The emoji is already the finished artwork: drawing a tint over it
        // with `.sourceAtop` repaints every pixel it covers, which is how a
        // full-colour sweet potato arrived in the menu bar as a flat purple
        // blob. The same mistake as `.renderingMode(.template)` in the popover
        // header, in a second place.
        // The emoji is finished artwork and carries its own palette, so it is
        // never tinted. Painting a state colour over it with `.sourceAtop`
        // repaints every pixel it covers, which is how a full-colour sweet
        // potato once arrived in the menu bar as a flat purple blob. Now that
        // every state draws the mark, that applies to every state.
        if Theme.Colors.sweetPotato {
            return base
        }

        let tinted = NSImage(size: base.size, flipped: false) { rect in
            base.draw(in: rect)
            tint(for: state).setFill()
            rect.fill(using: .sourceAtop)
            return true
        }
        // Not a template: a template image is recoloured by the system to match
        // the menu bar, which would throw away the state colour entirely.
        tinted.isTemplate = false
        return tinted
    }

    private static func tint(for state: GogumaState) -> NSColor {
        switch state {
        // Cold hearth when there is nothing to do, lit when there is.
        case .idle: NSColor(Theme.Colors.brandFill)
        case .holding: NSColor(Theme.Colors.emberFill)
        case .cutout: NSColor(Theme.Colors.danger)
        case .paused, .disconnected: NSColor(Theme.Colors.textSecondary)
        }
    }
}
