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
    /// Terminal rather than in-process, deliberately. Installing the privileged
    /// helper needs `sudo`, and the CLI refuses to guess at that: driving a
    /// password prompt from a menu bar popover means either an authorization
    /// dialog the user cannot connect to anything they typed, or asking for a
    /// password inside an app window, which is the shape of a phishing prompt
    /// and a habit no tool should teach. Terminal shows the real `sudo`, the
    /// real steps, and the output when it goes wrong.
    ///
    /// Via a `.command` file rather than AppleScript, which is what this used to
    /// do and why the button appeared to do nothing at all. Telling Terminal to
    /// `do script` is an Apple Event, and sending one requires
    /// `NSAppleEventsUsageDescription` in the bundle's Info.plist. That key was
    /// missing, so macOS refused the event outright: no consent dialog, no
    /// Terminal, no error the user could see. Opening a document needs no such
    /// permission, and a `.command` file is a document macOS opens in Terminal.
    @MainActor
    static func runInstaller() {
        guard let cli = bundledCLI else { return }

        let dir = FileManager.default.temporaryDirectory
        let script = dir.appending(path: "goguma-setup.command")

        // Quoted: the bundle lives wherever it was dragged, and
        // "/Users/x/Downloads/My Apps/goguma.app" is an ordinary path.
        let quoted = "'" + cli.path.replacingOccurrences(of: "'", with: "'\\''") + "'"
        let body = """
            #!/bin/bash
            clear
            \(quoted) install
            status=$?
            echo
            if [ $status -ne 0 ]; then
              echo "Setup did not finish. The output above says why,"
              echo "and nothing has been left running."
            fi
            """

        do {
            try body.write(to: script, atomically: true, encoding: .utf8)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o755], ofItemAtPath: script.path
            )
        } catch {
            // Nothing left that can open a terminal for them, so show them the
            // binary instead: a button that does nothing reads as a broken app,
            // and a Finder window at least says where the thing is.
            NSLog("goguma: couldn't write the setup script (\(error)); revealing the CLI")
            NSWorkspace.shared.activateFileViewerSelecting([cli])
            return
        }

        let config = NSWorkspace.OpenConfiguration()
        config.activates = true
        NSWorkspace.shared.open(script, configuration: config) { _, error in
            guard let error else { return }
            NSLog("goguma: couldn't open the setup script (\(error)); revealing the CLI")
            DispatchQueue.main.async {
                NSWorkspace.shared.activateFileViewerSelecting([cli])
            }
        }
    }
}
