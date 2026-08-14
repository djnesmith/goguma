import SwiftUI

/// Run history for one job: the duration series against its ceiling, then the
/// runs themselves.
///
/// The column that matters most is "Held", not "Duration". A job that runs for
/// 3 seconds while the Mac stays awake for four minutes is the failure mode
/// goguma exists to catch, and those rows are called out explicitly.
struct HistoryWindowView: View {
    @Bindable var store: StatusStore
    let jobID: String

    /// How many runs to request. The daemon caps its own retention well below
    /// this, so it is a ceiling rather than a page size.
    private static let runLimit = 100

    @State private var response: HistoryResponse?
    @State private var loadError: DaemonError?
    @State private var isLoading = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider()
            content
        }
        .frame(
            minWidth: Theme.Surface.historyWindowMinSize.width,
            minHeight: Theme.Surface.historyWindowMinSize.height
        )
        .themeSurface()
        .pollsDaemon(store, as: .historyWindow)
        .task(id: jobID) { await load() }
    }

    // MARK: - Header

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(response?.job.displayName ?? jobID)
                    .font(Theme.Typography.title)
                    .foregroundStyle(Theme.Colors.textPrimary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let response {
                    // The English rendering, not the cron expression. The raw
                    // form is still reachable in the tooltip for anyone who
                    // wants to check what was actually registered.
                    Text(response.scheduleText)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .textSelection(.enabled)
                        .help(response.job.schedule)
                }
            }
            // A real minimum, not zero: the job name is user-chosen and can be
            // any length, and a collapsible spacer lets it run into the badge.
            Spacer(minLength: Theme.Space.md)
            if let job = response?.job {
                DetectionBadge(mode: job.detection)
            }
            Button("Refresh", systemImage: Theme.Icon.refresh) {
                Task { await load() }
            }
            .labelStyle(.iconOnly)
            .buttonStyle(.bordered)
            .disabled(isLoading)
        }
        .padding(Theme.Space.md)
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        if store.connection.blocksContent || (loadError?.isUnreachable ?? false) {
            DaemonUnavailableView(error: loadError ?? store.connection.error) {
                Task { await load() }
            }
        } else if let error = loadError {
            DaemonUnavailableView(error: error) { Task { await load() } }
        } else if let response {
            VStack(alignment: .leading, spacing: Theme.Space.md) {
                summary(response.stats)
                RunDurationChart(
                    runs: chronological(response.runs),
                    ceiling: response.stats.ceiling,
                    observable: isObservable,
                    // The window opens before the job fires, so held time
                    // includes this. Without it the reference line is a
                    // different measurement from the bars it sits among.
                    // The job's own buffer wins when set: the daemon holds
                    // by it, and using the global default drew the line
                    // ~8 minutes under every bar for a job configured with
                    // a longer buffer.
                    wakeBuffer: response.job.wakeBuffer.isZero
                        ? (store.config?.wakeBuffer ?? .zero)
                        : response.job.wakeBuffer
                )
                .padding(.horizontal, Theme.Space.md)

                Divider()
                runsTable(response.runs)
            }
            .padding(.top, Theme.Space.md)
        } else {
            EmptyStateView(
                icon: Theme.Icon.history,
                title: isLoading ? "Loading history…" : "No history",
                detail: isLoading ? nil : "This job hasn't run since goguma started watching it."
            )
        }
    }

    /// Whether this job's runtime is measured at all. Wake-only jobs are never
    /// observed, so `typical` and the slow-run figure are structurally empty
    /// and showing them
    /// as blank statistics implies data that is merely missing.
    private var isObservable: Bool {
        response?.job.detection.isObservable ?? true
    }

    @ViewBuilder
    private func summary(_ stats: Stats) -> some View {
        HStack(alignment: .top, spacing: Theme.Space.xl) {
            statBlock("Runs", "\(stats.runs)", tint: Theme.Colors.textPrimary)

            if isObservable {
                statBlock(
                    "Typical", stats.typical.displayOrNil ?? Format.empty,
                    tint: Theme.Colors.textPrimary
                )
                statBlock(
                    "Slow run", stats.p95.displayOrNil ?? Format.empty,
                    tint: Theme.Colors.textPrimary
                )
                statBlock(
                    "Ceiling", stats.ceiling.displayOrNil ?? Format.empty,
                    tint: stats.coldStart ? Theme.Colors.textTertiary : Theme.Colors.textPrimary,
                    note: stats.coldStart ? "default, not enough history" : nil
                )
            } else {
                statBlock(
                    "Hold window", stats.ceiling.displayOrNil ?? Format.empty,
                    tint: Theme.Colors.textPrimary,
                    note: "fixed, the job can't be observed"
                )
            }

            statBlock(
                "Battery / run",
                stats.batteryPerRunText ?? "none",
                tint: stats.batteryPerRun > 0 ? Theme.Colors.warning : Theme.Colors.textPrimary,
                note: "average charge one firing costs"
            )
            Spacer(minLength: 0)
        }
        .padding(.horizontal, Theme.Space.md)
    }

    /// For a watched job, unaccounted hold time is a defect worth fixing. For a
    /// wake-only job it is the documented price of the mode, so flagging it in
    /// warning colour every time would be nagging about a correct setup.
    private func costTint(_ stats: Stats) -> Color {
        stats.batteryPerRun > 0 ? Theme.Colors.warning : Theme.Colors.textPrimary
    }

    private func statBlock(
        _ label: String, _ value: String, tint: Color, note: String? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            SectionLabel(text: label)
            Text(value)
                .font(Theme.Typography.headline)
                .foregroundStyle(tint)
            if let note {
                Text(note)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textTertiary)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private func runsTable(_ runs: [Run]) -> some View {
        Table(runs) {
            TableColumn("Started") { run in
                Text(run.timestamp.map(Format.timestamp) ?? Format.empty)
                    .font(Theme.Typography.tabularSmall)
                    .foregroundStyle(Theme.Colors.textSecondary)
            }
            .width(min: 150, ideal: 180)

            TableColumn("Duration") { run in
                Text(run.duration.displayOrNil ?? Format.empty)
                    .font(Theme.Typography.tabularSmall)
                    .foregroundStyle(Theme.Colors.textPrimary)
            }
            .width(min: 70, ideal: 80)

            TableColumn("Held") { run in
                HStack(spacing: Theme.Space.xs) {
                    Text(run.holdDuration.displayOrNil ?? Format.empty)
                        .font(Theme.Typography.tabularSmall)
                        .foregroundStyle(
                            run.isCostly ? Theme.Colors.warning : Theme.Colors.textPrimary
                        )
                    if run.isCostly {
                        Image(systemName: Theme.Icon.warning)
                            .font(Theme.Typography.iconInline)
                            .foregroundStyle(Theme.Colors.warning)
                            .help(
                                "Sleep was blocked \(run.holdDuration.displayString) for a job that "
                                    + "ran \(run.duration.displayString), costing \(run.drained)% of "
                                    + "battery. Usually a detection or wake-buffer problem."
                            )
                    }
                }
            }
            .width(min: 90, ideal: 110)

            TableColumn("Battery") { run in
                // What the run actually cost, rather than a duration standing
                // in for it. A run on AC shows nothing at all: it did not cost
                // charge, and a "0%" would read as a measurement rather than
                // as there being nothing to measure.
                Text(run.measuredOnBattery ? "\(run.drained)%" : Format.empty)
                    .font(Theme.Typography.tabularSmall)
                    .foregroundStyle(
                        run.isCostly ? Theme.Colors.warning : Theme.Colors.textTertiary
                    )
                    .help(
                        run.measuredOnBattery
                            ? "Charge used while the Mac was held awake for this run."
                            : "ran on AC power, so it cost no battery."
                    )
            }
            .width(min: 70, ideal: 80)

            TableColumn("Outcome") { run in
                OutcomeBadge(outcome: run.outcome)
            }
            .width(min: 110, ideal: 130)

            TableColumn("Exit") { run in
                Text(run.exitCode.map(String.init) ?? Format.empty)
                    .font(Theme.Typography.tabularSmall)
                    .foregroundStyle(
                        (run.exitCode ?? 0) == 0 ? Theme.Colors.textTertiary : Theme.Colors.danger
                    )
            }
            .width(min: 40, ideal: 50)

            TableColumn("Woke Mac") { run in
                Image(systemName: run.wokeMachine ? Theme.Icon.ok : Theme.Icon.outcomeUnknown)
                    .font(Theme.Typography.iconInline)
                    .foregroundStyle(
                        run.wokeMachine ? Theme.Colors.ok : Theme.Colors.textTertiary
                    )
                    .help(
                        run.wokeMachine
                            ? "The Mac was asleep and goguma woke it, so this run would have been missed."
                            : "The Mac was already awake."
                    )
            }
            .width(min: 60, ideal: 70)
        }
        .tableStyle(.inset)
    }

    // MARK: - Loading

    /// The daemon returns history newest-first; the chart reads left-to-right in
    /// time, so it gets the reverse.
    private func chronological(_ runs: [Run]) -> [Run] {
        runs.sorted { lhs, rhs in
            (lhs.timestamp ?? .distantPast) < (rhs.timestamp ?? .distantPast)
        }
    }

    private func load() async {
        guard !jobID.isEmpty else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            response = try await store.daemon.history(ref: jobID, limit: Self.runLimit)
            loadError = nil
        } catch let error as DaemonError {
            loadError = error
        } catch {
            loadError = .io(String(describing: error))
        }
    }
}
