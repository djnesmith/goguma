import AppKit
import Foundation

/// First-run setup for someone who arrived via the download rather than the
/// command line.
///
/// The app is a viewer: it reads the daemon over a unix socket and holds no
/// state of its own. Someone who installed the CLI already has a daemon by the
/// time they ever see this app. Someone who downloaded the disk image has
/// neither, and used to be told "Run `goguma install` in Terminal" — advice
/// that cannot be followed, because `goguma` is not on their PATH. It is inside
/// this bundle.
///
/// So the bundle carries the four binaries (see macos/scripts/make-app.sh) and
/// this offers to run the installer out of them.
enum Onboarding {

    /// The bundled `goguma` executable, when there is one.
    ///
    /// nil for a loose `swift run` during development, where the surrounding
    /// bundle is SwiftPM's and has no Resources/bin. The button is hidden in
    /// that case rather than offering to run something that is not there.
    static var bundledCLI: URL? {
        guard let resources = Bundle.main.resourceURL else { return nil }
        let cli = resources.appending(path: "bin/goguma")
        return FileManager.default.isExecutableFile(atPath: cli.path) ? cli : nil
    }

    /// Whether setup can be offered from inside the app.
    static var canSelfInstall: Bool { bundledCLI != nil }

    /// Whether the popover has already introduced itself on a launch that
    /// found no daemon.
    ///
    /// Persisted, so this happens once on the machine rather than once per
    /// process. A failed or abandoned setup leaves the daemon absent, and
    /// without this the app would present itself at every login forever.
    static var hasPresentedFirstRun: Bool {
        get { UserDefaults.standard.bool(forKey: firstRunKey) }
        set { UserDefaults.standard.set(newValue, forKey: firstRunKey) }
    }

    private static let firstRunKey = "goguma.hasPresentedFirstRun"

    /// What to tell a disconnected user, which depends on how they got here.
    static var disconnectedAdvice: String {
        canSelfInstall
            ? "goguma needs to set up its background service."
            : "Run `goguma install` in Terminal."
    }

    /// Opens Terminal and runs the bundled installer.
    ///
    /// Deliberately Terminal rather than running it in-process. Installing the
    /// privileged helper needs `sudo`, and the CLI already refuses to guess at
    /// that: it prints "Run this in Terminal: a password cannot be entered
    /// without one" and stops. Driving a password prompt from a menu bar popover
    /// would mean either an authorization dialog the user cannot connect to
    /// anything they typed, or asking for their password inside an app window,
    /// which is exactly the shape of a phishing prompt and a habit no tool
    /// should teach. Terminal shows the real `sudo`, the real steps, and the
    /// output when it goes wrong.
    @MainActor
    static func runInstaller() {
        guard let cli = bundledCLI else { return }

        // Quoted for the shell: the bundle lives wherever it was dragged, and
        // "/Users/x/Downloads/My Apps/goguma.app" is a perfectly ordinary path.
        let quoted = "'" + cli.path.replacingOccurrences(of: "'", with: "'\\''") + "'"
        let script = """
            tell application "Terminal"
                activate
                do script "clear && \(quoted) install"
            end tell
            """

        guard let apple = NSAppleScript(source: script) else { return }
        var error: NSDictionary?
        apple.executeAndReturnError(&error)
        if let error {
            // Automating Terminal needs consent the user may have refused, and
            // a button that silently does nothing reads as a broken app. Fall
            // back to revealing the binary so there is still a way forward.
            NSLog("goguma: could not drive Terminal (\(error)); revealing the CLI instead")
            NSWorkspace.shared.activateFileViewerSelecting([cli])
        }
    }
}
