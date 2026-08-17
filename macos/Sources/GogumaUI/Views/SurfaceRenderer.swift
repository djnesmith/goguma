import AppKit
import SwiftUI

/// Renders a surface to a PNG and exits. Development tooling; nothing in the
/// shipping app calls it.
///
/// This exists because the UI is otherwise unreviewable from a terminal. There
/// is no way to screenshot a running menu bar app without screen-recording
/// permission, and every visual defect this project has shipped (a popover
/// clipped off the top of the screen, a title colliding with a counter, an
/// entire palette that never reached the screen, a dark-mode illustration of
/// animals floating in a void) was invisible in the source and obvious in a
/// picture. Reasoning about layout code is not a substitute for looking at it.
///
/// **Why an offscreen window rather than `ImageRenderer`.** `ImageRenderer`
/// only draws what SwiftUI itself draws. `List`, `Form(.grouped)`, `Table` and
/// `ScrollView` are all AppKit-backed, so they came out as blank panels or as
/// SwiftUI's yellow "unsupported" placeholder, which is to say, every
/// container this app actually uses. Hosting the view in a real window placed
/// far offscreen and capturing it with `cacheDisplay` renders the genuine
/// AppKit output, at the cost of needing the run loop to settle first.
///
/// Data comes from the live daemon, so what is rendered is what a real user
/// with real jobs would see rather than a flattering fixture.
@MainActor
enum SurfaceRenderer {
    static let surfaces = [
        "popover", "jobs", "jobs-selected", "settings", "addjob", "history", "empty", "offline",
        "marks",
    ]

    static func run(surface: String, path: String, dark: Bool) async {
        let store = StatusStore()
        // `refresh` only fetches the job list when a surface that needs it has
        // registered, which normally happens via `.pollsDaemon`. Nothing is on
        // screen here, so register by hand or every surface renders empty.
        store.beginObserving(.jobsWindow)
        await store.refresh()
        await store.loadConfig()

        let coordinator = WindowCoordinator(store: store)
        let (requested, content) = build(surface: surface, store: store, coordinator: coordinator)

        let host = NSHostingController(rootView: content)
        // No safe area. The window below is `.titled`, and a hosting controller
        // in a titled window inherits a top inset the height of the titlebar,
        // which the real popover does not have, because it is not in a titled
        // window. Every render came out with roughly 52pt of blank paper above
        // the headline, and that band shipped into every screenshot on the
        // landing page.
        host.safeAreaRegions = []
        var size = requested
        if size.height == 0 {
            // Probed tall, not one point tall. At height 1 a wrapping caption
            // is asked to lay out in a space it cannot fit, and reports back
            // more height than it uses once it has room, which lands as an
            // unpainted band above a content-sized surface.
            host.view.frame = CGRect(x: 0, y: 0, width: size.width, height: 4000)
            host.view.layoutSubtreeIfNeeded()
            size.height = max(host.view.fittingSize.height, 1)
        }
        host.view.frame = CGRect(origin: .zero, size: size)

        // Far offscreen: AppKit treats it as on screen and lays out table and
        // scroll views properly, but no human ever sees it.
        let window = NSWindow(
            contentRect: CGRect(x: -20000, y: -20000, width: size.width, height: size.height),
            styleMask: [.titled, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.contentViewController = host
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.titlebarAppearsTransparent = true
        window.isOpaque = true
        window.orderFrontRegardless()

        // Nothing focused, the same as `WindowCoordinator` does for the real
        // window as soon as it shows one.
        //
        // AppKit hands first responder to the first text field in a new window,
        // which in Settings is the temperature box, so every render of that
        // surface came out with an accent ring round a field nobody had
        // clicked, and it reached the README that way. The real window has
        // never had the problem; this window was just missing the same line.
        // After ordering front, because that is when the assignment happens.
        window.makeFirstResponder(nil)

        // Let layout and the first render settle: lists and forms populate a
        // frame or two after the window appears. `RunLoop.run` is unavailable
        // from an async context, so yield through the main-actor scheduler
        // instead, which pumps the loop just the same.
        for _ in 0..<60 {
            try? await Task.sleep(for: .milliseconds(10))
        }

        // Re-measure once the window is real.
        //
        // The first measurement happens on a detached view one point tall,
        // where wrapping text has no width to wrap against and reports more
        // height than it finally uses. For a surface that sizes itself to its
        // content that over-estimate becomes an unpainted band along one edge,
        // a defect in the screenshot that does not exist in the app, which is
        // worse than useless when the screenshot is the thing being reviewed.
        if requested.height == 0 {
            let settled = host.view.fittingSize.height
            if settled > 1, abs(settled - size.height) > 0.5 {
                size.height = settled
                window.setContentSize(NSSize(width: size.width, height: settled))
                host.view.frame = CGRect(origin: .zero, size: size)
                host.view.layoutSubtreeIfNeeded()
                for _ in 0..<20 {
                    try? await Task.sleep(for: .milliseconds(10))
                }
            }
        }

        guard let view = window.contentView else {
            fail("no content view")
            return
        }
        view.layoutSubtreeIfNeeded()

        guard let data = capture(view) else {
            fail("couldn't encode PNG")
            return
        }
        do {
            try data.write(to: URL(fileURLWithPath: path))
            print("wrote \(path)  [\(surface), \(dark ? "dark" : "light"), \(store.jobs.count) jobs]")
        } catch {
            fail("write failed: \(error)")
        }
        window.close()
    }

    private static func build(
        surface: String, store: StatusStore, coordinator: WindowCoordinator
    ) -> (CGSize, AnyView) {
        switch surface {
        case "jobs", "jobs-selected":
            return (
                Theme.Surface.jobsWindowSize,
                AnyView(JobsWindowView(
                    store: store,
                    coordinator: coordinator,
                    initialSelection: surface == "jobs-selected"
                        ? store.sortedJobs.first?.id : nil
                ))
            )
        case "settings":
            // Height 0 means "measure it". The settings pane sizes its own
            // window to its content, so handing the renderer a constant would
            // photograph it at a height it never actually has, and any excess
            // shows up as an unpainted band, which is exactly the defect this
            // renderer exists to catch.
            return (
                CGSize(width: Theme.Surface.settingsWidth, height: 0),
                AnyView(SettingsWindowView(store: store))
            )
        case "addjob":
            // The one surface with no other way in: a sheet only reachable by
            // clicking a button in a window, which is exactly the kind of thing
            // that goes unreviewed until a user reports it.
            return (
                CGSize(width: 560, height: 0),
                AnyView(JobEditSheet(store: store, mode: .add) {}.themeSurface())
            )
        case "history":
            let id = store.sortedJobs.first?.job.id ?? ""
            return (
                Theme.Surface.historyWindowSize,
                AnyView(HistoryWindowView(store: store, jobID: id))
            )
        case "empty":
            return (
                CGSize(width: 900, height: 560),
                AnyView(
                    EmptyStateView(
                        icon: Theme.Icon.jobs,
                        title: "No jobs registered",
                        detail: "Add a job, or import the ones you already have with "
                            + "`goguma import`."
                    )
                    .themeSurface()
                )
            )
        case "marks":
            // A proof sheet for the menu bar mark: real size beside a blow-up,
            // because 16pt is the only size that matters and the only size at
            // which a design decision here can actually be judged.
            return (CGSize(width: 460, height: 200), AnyView(MarkProofSheet()))
        case "offline":
            return (
                CGSize(width: 900, height: 560),
                AnyView(DaemonUnavailableView(error: nil).themeSurface())
            )
        default:
            // Height 0 means "ask the view", the way NSPopover does. Forcing
            // the maximum here would have hidden the very bug this tool exists
            // to catch: the popover always rendering at full height.
            return (
                CGSize(width: Theme.Surface.popoverWidth, height: 0),
                AnyView(PopoverView(store: store, coordinator: coordinator))
            )
        }
    }

    /// Captures the view.
    ///
    /// **Known gap:** `Form(.formStyle(.grouped))` captures as a blank panel:
    /// its chrome draws and its contents do not. `CALayer.render(in:)` was
    /// tried as the fix and is worse: it returns an entirely empty image,
    /// losing even the parts `cacheDisplay` gets. So the Settings pane is the
    /// one surface this tool cannot show, and it still has to be reviewed by
    /// reading it. Everything else (popover, jobs, history, empty states)
    /// renders faithfully, including `List`.
    /// Pixels per point in the captured image.
    ///
    /// `bitmapImageRepForCachingDisplay` sizes its rep from the window's backing
    /// scale, which on this machine is 2, so a 340pt popover came out 680px,
    /// and the landing page displays it wider than that. Anything shown larger
    /// than its own pixel count is upscaled, which is exactly the softness that
    /// makes a product shot look cheap.
    ///
    /// Building the rep by hand at 3x and then setting `size` back to the point
    /// size is what makes `cacheDisplay` draw at that scale rather than tile.
    /// 4, not 3. The landing page shows the popover at 340 CSS px, which is 680
    /// device pixels on a 2x display, and 4x gives exactly 1360, a clean 2:1
    /// downscale. At 3x it was 1020 into 680, a 0.667 resample that visibly
    /// softens 11px UI text however good the filter is.
    private static let captureScale: CGFloat = 4

    private static func capture(_ view: NSView) -> Data? {
        let bounds = view.bounds
        let px = NSSize(
            width: (bounds.width * captureScale).rounded(),
            height: (bounds.height * captureScale).rounded()
        )
        guard px.width >= 1, px.height >= 1,
              let rep = NSBitmapImageRep(
                  bitmapDataPlanes: nil,
                  pixelsWide: Int(px.width), pixelsHigh: Int(px.height),
                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
              )
        else {
            return nil
        }
        rep.size = bounds.size
        view.cacheDisplay(in: bounds, to: rep)
        return rep.representation(using: .png, properties: [:])
    }

    private static func fail(_ message: String) {
        FileHandle.standardError.write(Data("render failed: \(message)\n".utf8))
    }
}
