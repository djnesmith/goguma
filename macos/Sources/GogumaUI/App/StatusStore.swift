import Observation
import SwiftUI

/// The coarse state the whole UI keys off: one line in the popover, one glyph
/// in the menu bar, one empty state in every window.
enum GogumaState: Sendable, Hashable {
    /// The daemon could not be reached at all.
    case disconnected
    /// The daemon is reachable but the user has paused it.
    case paused
    /// A thermal or low-battery cutout has fired and is still latched.
    case cutout
    /// At least one wake window is open; sleep is blocked.
    case holding
    /// Reachable, running, nothing to hold.
    case idle

    var iconName: String {
        switch self {
        case .disconnected: Theme.Icon.disconnected
        case .paused: Theme.Icon.paused
        case .cutout: Theme.Icon.cutout
        case .holding: Theme.Icon.holding
        case .idle: Theme.Icon.idle
        }
    }

    var tint: Color {
        switch self {
        case .disconnected: Theme.Colors.stateIdle
        case .paused: Theme.Colors.statePaused
        case .cutout: Theme.Colors.stateCutout
        case .holding: Theme.Colors.stateHolding
        case .idle: Theme.Colors.stateIdle
        }
    }

    /// Short label for the menu bar's accessibility description, its tooltip,
    /// and the right-click menu header. Phrased to match the CLI's wording so
    /// the two surfaces describe the same state the same way.
    var label: String {
        switch self {
        case .disconnected: "goguma isn't running"
        case .paused: "paused"
        case .cutout: "safety cutout fired"
        case .holding: "holding sleep off"
        case .idle: "on watch · your Mac can sleep"
        }
    }
}

/// How the last daemon conversation went.
enum ConnectionState: Sendable, Equatable {
    /// Nothing has been attempted yet.
    case connecting
    case connected
    /// The daemon isn't there. This is the "run `goguma install`" case.
    case unreachable(DaemonError)
    /// Version skew. Rendering the payload would misinform, so we don't.
    case mismatch(DaemonError)
    /// Reachable but the last call failed for some other reason.
    case failing(DaemonError)

    var error: DaemonError? {
        switch self {
        case .connecting, .connected: nil
        case let .unreachable(e), let .mismatch(e), let .failing(e): e
        }
    }

    /// True when no data can be shown and the window should render its empty
    /// state instead of a half-populated view.
    var blocksContent: Bool {
        switch self {
        case .connected: false
        case .connecting, .unreachable, .mismatch, .failing: true
        }
    }
}

/// Which surfaces currently want fresh data. Polling runs at the fast cadence
/// while any of these is on screen.
enum PollingSurface: Hashable, Sendable {
    case popover
    case jobsWindow
    case historyWindow
    case settingsWindow

    /// Whether this surface needs the (heavier) `jobs.list` payload as well as
    /// `status`.
    ///
    /// Settings needs it despite showing no job list: the only way this app can
    /// name the scheduler sources it may re-enable automatic adoption with is by
    /// reading them off the registered jobs, and an empty list there would
    /// disable the toggle for no visible reason.
    var needsJobs: Bool {
        switch self {
        case .popover, .jobsWindow, .settingsWindow: true
        case .historyWindow: false
        }
    }
}

/// The single source of truth for daemon state, owned by the main actor.
///
/// Everything here is read by SwiftUI and written only after an `await` on a
/// `DaemonClient` call, so no view ever touches the socket and no socket work
/// ever touches the main thread.
@MainActor
@Observable
final class StatusStore {

    // MARK: - Published state

    private(set) var status: DaemonStatus?
    private(set) var jobs: [JobView] = []
    /// Group names in use, sorted, straight from the daemon.
    private(set) var groups: [String] = []
    private(set) var config: DaemonConfig?
    private(set) var configWarnings: [String] = []

    /// The daemon's own answer for the charge below which it will not
    /// schedule a wake. Not derived here; see `ConfigResponse`.
    private(set) var wakeFloorBasePct: Int = 0
    /// Whether the daemon can verify an advisory feed at all.
    private(set) var advisoriesAvailable = false
    private(set) var connection: ConnectionState = .connecting
    private(set) var lastUpdated: Date?

    /// Ticks once a second while any surface is visible, so live counters
    /// (elapsed hold, countdown to next wake) advance without each view running
    /// its own timer.
    private(set) var now: Date = .now

    /// The result of the most recent user-initiated action, shown inline so a
    /// failed "Skip next wake" isn't silent.
    private(set) var actionMessage: ActionMessage?

    /// True while a user-initiated action is in flight; disables the buttons.
    private(set) var isPerformingAction = false

    struct ActionMessage: Sendable, Equatable {
        var text: String
        var isError: Bool
    }

    // MARK: - Derived state

    var state: GogumaState {
        guard let status, !connection.blocksContent else { return .disconnected }
        if let cutout = status.cutout, cutout.latched { return .cutout }
        if status.paused { return .paused }
        if status.holding || !status.holds.isEmpty { return .holding }
        return .idle
    }

    /// The wake time to advertise in the menu bar, if one is scheduled.
    var nextWake: Date? { status?.nextWake.meaningful }

    /// Non-empty when a wake is being deliberately withheld. Rendered as
    /// information, never as a failure; it is a safeguard doing its job.
    var wakeSuppressed: String {
        guard let status, !connection.blocksContent else { return "" }
        return status.wakeSuppressed
    }

    /// Why nothing is scheduled, when the reason is not the battery.
    var noWakeReason: String {
        guard let status, !connection.blocksContent else { return "" }
        return status.noWakeReason
    }

    /// Warnings worth putting in front of the user, most severe first.
    /// See `DaemonStatus.actionableWarnings` for the promotion rules.
    var warnings: [DaemonWarning] {
        status?.actionableWarnings ?? []
    }

    /// Jobs sorted for display: soonest next run first, disabled ones last.
    var sortedJobs: [JobView] {
        jobs.sorted { lhs, rhs in
            if lhs.job.enabled != rhs.job.enabled { return lhs.job.enabled }
            let l = lhs.nextFire.meaningful ?? .distantFuture
            let r = rhs.nextFire.meaningful ?? .distantFuture
            if l != r { return l < r }
            return lhs.job.displayName.localizedCaseInsensitiveCompare(rhs.job.displayName) == .orderedAscending
        }
    }

    /// Jobs bucketed by group, in the order a list should render them.
    ///
    /// Named groups first in alphabetical order, then ungrouped jobs under an
    /// empty key. Ungrouped goes last on the reasoning that a user who has
    /// bothered to file something has said it matters; the unfiled remainder is
    /// the inbox, and an inbox belongs at the bottom.
    var groupedJobs: [(group: String, jobs: [JobView])] {
        var buckets: [String: [JobView]] = [:]
        for view in sortedJobs {
            buckets[view.job.group, default: []].append(view)
        }
        let named = buckets.keys
            .filter { !$0.isEmpty }
            .sorted { $0.localizedCaseInsensitiveCompare($1) == .orderedAscending }
        var out = named.map { (group: $0, jobs: buckets[$0] ?? []) }
        if let ungrouped = buckets[""], !ungrouped.isEmpty {
            out.append((group: "", jobs: ungrouped))
        }
        return out
    }

    /// True when at least one job is filed, so the UI knows whether section
    /// headers would be telling the user anything.
    var usesGroups: Bool {
        !groups.isEmpty || jobs.contains { !$0.job.group.isEmpty }
    }

    func job(withID id: String) -> JobView? {
        jobs.first { $0.job.id == id }
    }

    // MARK: - Change notification
    //
    // AppKit owns the status item, which is not a SwiftUI view and so cannot
    // observe this model. It registers here instead.

    var onChange: (@MainActor () -> Void)?

    // MARK: - Polling

    private var surfaces: [PollingSurface: Int] = [:]
    private var pollTask: Task<Void, Never>?
    private let client: DaemonClient

    init(client: DaemonClient = .shared) {
        self.client = client
    }

    /// Registers a visible surface. Polling switches to the fast cadence and
    /// refreshes immediately, so opening a window never shows stale data.
    func beginObserving(_ surface: PollingSurface) {
        surfaces[surface, default: 0] += 1
        startPolling(immediately: true)
    }

    /// Deregisters a surface. When the last one goes, polling drops back to the
    /// slow menu-bar cadence.
    func endObserving(_ surface: PollingSurface) {
        guard let count = surfaces[surface] else { return }
        if count <= 1 {
            surfaces.removeValue(forKey: surface)
            // The popover has just closed having shown whatever notices were
            // pending, so they have been seen.
            //
            // On close rather than on open: acking as it appears would clear
            // the notice the same second it rendered, and the next poll a
            // second later would erase it while the user was still reading.
            if surface == .popover, hasDismissibleNotice {
                Task { try? await client.ackNotices() }
            }
        } else {
            surfaces[surface] = count - 1
        }
        startPolling(immediately: false)
    }

    /// Whether anything pending is the kind that gets dismissed by being read.
    ///
    /// Checked so the common case, a popover opened to look at the next wake,
    /// does not send a message on every close.
    private var hasDismissibleNotice: Bool {
        warnings.contains { $0.kind == .retired }
    }

    private var hasVisibleSurface: Bool { !surfaces.isEmpty }

    private var wantsJobs: Bool {
        surfaces.keys.contains { $0.needsJobs }
    }

    /// Starts (or restarts) the poll loop.
    ///
    /// The loop never stops entirely: the menu bar item is always on screen and
    /// an icon that never refreshes is a broken icon. It does drop from
    /// `activePollInterval` to `idlePollInterval` (see `Theme.Timing`), which
    /// is the battery-conscious part.
    func startPolling(immediately: Bool = true) {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            if immediately {
                await self?.refresh()
            }
            while !Task.isCancelled {
                let interval = await self?.pollInterval ?? Theme.Timing.idlePollInterval
                do {
                    try await Task.sleep(for: interval)
                } catch {
                    return // cancelled
                }
                guard !Task.isCancelled else { return }
                await self?.refresh()
            }
        }
    }

    /// How long to wait before the next poll.
    ///
    /// Read each time round rather than captured once, which was a bug of its
    /// own: a loop started while nothing was on screen kept the idle interval
    /// after a window opened, until something else restarted it.
    ///
    /// The middle case is why this exists. Thirty seconds is right for a
    /// machine with nothing happening, and wrong for one that is holding sleep
    /// off: opening the menu bar then showed state up to half a minute old, so
    /// "holding for …" was missing at the moment of the click and appeared a
    /// beat later when the refresh landed. While a hold is open the state is
    /// both interesting and changing, and five seconds costs nothing on a
    /// machine that is already awake because of it.
    private var pollInterval: Duration {
        if hasVisibleSurface { return Theme.Timing.activePollInterval }
        if status?.holds.isEmpty == false { return Theme.Timing.holdingPollInterval }
        return Theme.Timing.idlePollInterval
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    // MARK: - Fetching

    /// One poll cycle: status, plus jobs when a surface that needs them is up.
    func refresh() async {
        now = .now
        do {
            let fresh = try await client.status()
            status = fresh
            lastUpdated = .now
            connection = .connected
        } catch let error as DaemonError {
            apply(error)
        } catch {
            apply(DaemonError.io(String(describing: error)))
        }

        guard case .connected = connection else { return }

        if wantsJobs {
            do {
                let response = try await client.jobs()
                jobs = response.jobs
                groups = response.groups
            } catch let error as DaemonError {
                // A failed jobs fetch must not blank the status line, which is
                // the more important of the two. Record it and keep the last
                // known list.
                if error.isUnreachable { apply(error) }
            } catch {
                // Ignore: status already told us the daemon is alive.
            }
        }
        notify()
    }

    /// Loads config on demand. Settings is the only surface that needs it, and
    /// it changes only when the user changes it, so it is not part of the poll.
    func loadConfig() async {
        do {
            let response = try await client.config()
            config = response.config
            configWarnings = response.warnings
            wakeFloorBasePct = response.wakeFloorBasePct
            advisoriesAvailable = response.advisoriesAvailable
            connection = .connected
        } catch let error as DaemonError {
            apply(error)
        } catch {
            apply(DaemonError.io(String(describing: error)))
        }
    }

    private func apply(_ error: DaemonError) {
        switch error {
        case .protocolMismatch:
            connection = .mismatch(error)
            // Refusing to render is the point: a payload from a different
            // protocol version would be shown wrong, not shown late.
            status = nil
            jobs = []
            config = nil
        case .notInstalled, .notRunning, .permissionDenied, .timedOut, .io:
            connection = .unreachable(error)
            status = nil
            jobs = []
        case .refused, .malformedResponse:
            connection = .failing(error)
        }
        notify()
    }

    // MARK: - Actions

    /// Runs a mutating call, surfaces its outcome, and refreshes so the UI
    /// reflects the new state immediately rather than up to a second later.
    func perform(
        _ description: String,
        _ work: @escaping @Sendable (DaemonClient) async throws -> Void
    ) {
        performReporting { client in
            try await work(client)
            return description
        }
    }

    /// As `perform`, but the success message comes from the call itself.
    ///
    /// Needed by anything whose result the user cares about rather than just
    /// its success: `sync` reports how many jobs it adopted or retired, and
    /// "Synced." would be throwing away the only interesting part.
    func performReporting(
        _ work: @escaping @Sendable (DaemonClient) async throws -> String
    ) {
        guard !isPerformingAction else { return }
        isPerformingAction = true
        actionMessage = nil
        let client = client
        Task { @MainActor in
            do {
                let message = try await work(client)
                actionMessage = ActionMessage(text: message, isError: false)
            } catch let error as DaemonError {
                actionMessage = ActionMessage(text: error.localizedDescription, isError: true)
            } catch {
                actionMessage = ActionMessage(text: String(describing: error), isError: true)
            }
            await refresh()
            isPerformingAction = false
        }
    }

    /// Writes one config key and re-reads the result, in that order.
    ///
    /// Deliberately not `perform`. That one is fire-and-forget, sets
    /// `isPerformingAction` for the duration, and drops any second call that
    /// arrives while the first is in flight, all of which are right for a
    /// button and wrong for a setting:
    ///
    /// - The caller has to re-read afterwards to reflect a clamped value, and
    ///   with a detached write that read raced the write and won, so a slider
    ///   released on 25 was set to 25 and then immediately redrawn at its old
    ///   value. It looked like the setting refused to take.
    /// - The whole pane is disabled while an action is in flight, so adjusting
    ///   one slider greyed out and un-greyed the entire window.
    /// - Two adjustments in quick succession meant the second was discarded
    ///   without a word, and then contradicted by the re-read.
    ///
    /// Sequential, scoped to nothing, and never dropped.
    @discardableResult
    func writeConfig(key: String, value: String) async -> Bool {
        do {
            try await client.setConfig(key: key, value: value)
        } catch let error as DaemonError {
            actionMessage = ActionMessage(text: error.localizedDescription, isError: true)
            return false
        } catch {
            actionMessage = ActionMessage(text: String(describing: error), isError: true)
            return false
        }
        await loadConfig()
        return true
    }

    func clearActionMessage() {
        actionMessage = nil
    }

    /// Reports the result of something the UI did on its own, with no daemon
    /// round trip, copying diagnostics to the pasteboard, for instance.
    /// `perform` covers everything that talks to the daemon.
    func note(_ text: String) {
        actionMessage = ActionMessage(text: text, isError: false)
    }

    /// Direct access for sheets that need a one-off call and their own error
    /// handling (the edit sheet's pattern tester, for instance).
    var daemon: DaemonClient { client }

    private func notify() {
        onChange?()
    }
}

// MARK: - Polling as a view modifier

private struct PollingSurfaceModifier: ViewModifier {
    let store: StatusStore
    let surface: PollingSurface

    func body(content: Content) -> some View {
        // Registration follows real window visibility, not SwiftUI's
        // appear/disappear. The popover's hosting view lives inside an
        // always-alive menu-bar window that is hidden with orderOut, so
        // onDisappear never fired: the popover registered once at launch
        // and polled jobs.list at the 1-second cadence forever, windows
        // closed, in an app whose whole polling design is battery-conscious
        // idle. Occlusion transitions cover both real windows (closing
        // deallocates them, reported as a detach) and the ordered-out case.
        content.background(WindowVisibilityReader { visible in
            if visible {
                store.beginObserving(surface)
            } else {
                store.endObserving(surface)
            }
        })
    }
}

/// Reports transitions of the hosting window's actual visibility. Calls the
/// handler only on changes, starting from "not visible", so begin/end pair
/// exactly against the store's refcounted registration.
private struct WindowVisibilityReader: NSViewRepresentable {
    let onChange: (Bool) -> Void

    func makeNSView(context _: Context) -> VisibilityTrackingView {
        let v = VisibilityTrackingView()
        v.onChange = onChange
        return v
    }

    func updateNSView(_ view: VisibilityTrackingView, context _: Context) {
        view.onChange = onChange
    }
}

final class VisibilityTrackingView: NSView {
    var onChange: ((Bool) -> Void)?
    private var lastReported = false
    private var observer: NSObjectProtocol?

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if let observer {
            NotificationCenter.default.removeObserver(observer)
            self.observer = nil
        }
        guard let window else {
            report(false)
            return
        }
        observer = NotificationCenter.default.addObserver(
            forName: NSWindow.didChangeOcclusionStateNotification,
            object: window, queue: .main
        ) { [weak self] _ in
            self?.reportCurrent()
        }
        reportCurrent()
    }

    private func reportCurrent() {
        report(window?.occlusionState.contains(.visible) == true)
    }

    private func report(_ visible: Bool) {
        guard visible != lastReported else { return }
        lastReported = visible
        onChange?(visible)
    }
}

extension View {
    /// Ties this view's visibility to the fast poll cadence. Every window and
    /// the popover apply this; nothing else may call `beginObserving` directly.
    func pollsDaemon(_ store: StatusStore, as surface: PollingSurface) -> some View {
        modifier(PollingSurfaceModifier(store: store, surface: surface))
    }
}

