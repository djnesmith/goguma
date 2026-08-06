import AppKit
import SwiftUI

/// Settings: the handful of things worth changing, and nothing else.
///
/// Every field writes through `config.set`, which takes a key and a *string*,
/// so each control produces the text form the daemon parses. Values are applied
/// one key at a time and the config is re-read afterwards, so a value the daemon
/// clamps shows its clamped value here rather than the one that was chosen.
///
/// **On explanation.** An earlier version carried a paragraph of rationale under
/// every section — why wake-only jobs get a fixed window, why powering on a
/// shut-down Mac is opt-in, what a duration string may contain. All of it was
/// true and none of it belonged here. A settings pane is read by someone who
/// has already decided to change something; prose between them and the control
/// is an obstacle, and a wall of it makes the few genuinely important warnings
/// impossible to pick out. The reasoning now lives in tooltips, where it is
/// asked for, and in `Docs/ARCHITECTURE.md`, where it belongs.
struct SettingsWindowView: View {
    @Bindable var store: StatusStore

    @State private var thermalCutout: Double = 80
    @State private var lowBatteryCutout: Double = 20
    @State private var webhookText = ""
    @State private var showAdvanced = false

    private static let thermalRange: ClosedRange<Double> = 70...95
    private static let batteryRange: ClosedRange<Double> = 5...50

    var body: some View {
        VStack(spacing: 0) {
            if store.connection.blocksContent {
                DaemonUnavailableView(error: store.connection.error) {
                    Task { await store.refresh(); await store.loadConfig() }
                }
            } else {
                // One page, no scroll, on the app's own surface.
                //
                // This was a `Form(.grouped)`, which brings its own opaque white
                // background and its own scroll view — so the pane looked like a
                // different application from every other window, and a settings
                // list short enough to fit scrolled anyway. Laying it out
                // directly keeps the arctic surface and makes the height
                // honest: if it ever stops fitting, that is a signal there are
                // too many settings, not a reason to add a scrollbar.
                // One `Grid` for the whole pane, not a stack per section.
                //
                // Mac settings put labels in a right-aligned column against a
                // left-aligned column of controls, and the column is as wide as
                // its longest label — no wider. This was a fixed 168pt guess
                // shared by hand between three row types, which is why "Let it
                // sleep if battery under" ran to the edge of its column while
                // "Status" left most of one empty, and why every control sat in
                // a narrow band with a third of the window blank to its right.
                //
                // A single grid measures the column once from the real strings,
                // so the sections align with each other rather than each being
                // internally consistent and mutually wrong.
                Grid(
                    alignment: .leadingFirstTextBaseline,
                    horizontalSpacing: Theme.Space.sm,
                    verticalSpacing: Theme.Space.xs
                ) {
                    timingSection
                    sectionRule
                    adoptionSection
                    sectionRule
                    safetySection
                    sectionRule
                    alertsSection
                    sectionRule
                    advancedSection
                    if !store.configWarnings.isEmpty { warningsSection }
                }
                .padding(.horizontal, Theme.Space.md)
                .padding(.top, Theme.Space.sm)
                .padding(.bottom, Theme.Space.md)
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            statusBar
        }
        .frame(width: Theme.Surface.settingsWidth)
        // Height comes from the content, not a constant.
        //
        // It was a fixed 580pt, which is what the pane needs with **Advanced**
        // open. Closed — which is how it always opens — that left ~100pt of
        // empty surface above the status bar, and a window with a band of
        // nothing at the bottom reads as one that failed to load. Sizing to the
        // content means the pane fits either state exactly, and opening the
        // disclosure grows the window rather than revealing space that was
        // always there.
        .modifier(FitsWindowHeight())
        // Paint whatever height we are actually given, even if it exceeds the
        // content. The window is sized from the measurement above, so normally
        // the two agree — but when a host asks for more (the offscreen renderer
        // does), the surface must still reach the edges. Without this the extra
        // showed up as an unpainted white band above the pane.
        .frame(maxHeight: .infinity, alignment: .top)
        .themeSurface()
        .pollsDaemon(store, as: .settingsWindow)
        .task {
            await store.loadConfig()
            loadFields()
        }
        .onChange(of: store.config) { _, _ in loadFields() }
    }

    /// A settings row: label, then its control on the shared column.
    ///
    /// Left-aligned, not right-aligned against the control column.
    ///
    /// Right-aligned labels are the Mac convention and they are correct — for a
    /// pane where every row has one. Over half of this pane is checkboxes,
    /// which carry their own text and take no label, so the right-aligned
    /// version left the entire left column empty beside them: "Tell me when a
    /// job gets missed" floating in mid-air with 450pt of nothing to its left.
    /// A convention that fits three rows and strands five is the wrong
    /// convention for this content.
    @ViewBuilder
    private func settingsRow(
        _ label: String, @ViewBuilder _ control: () -> some View
    ) -> some View {
        GridRow {
            Text(label)
                .font(Theme.Typography.body)
                .foregroundStyle(Theme.Colors.textPrimary)
                .gridColumnAlignment(.leading)
            control()
        }
    }

    /// A control that states its own case. It spans both columns so it begins
    /// at the left margin with everything else, rather than starting where the
    /// controls happen to start and leaving a hole where its label would be.
    @ViewBuilder
    private func unlabelledRow(@ViewBuilder _ control: () -> some View) -> some View {
        GridRow {
            control()
                .gridCellColumns(2)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// Full-width rule between sections.
    private var sectionRule: some View {
        GridRow {
            ThemeHairline()
                .gridCellColumns(2)
                .padding(.top, Theme.Space.sm)
        }
    }

    /// A titled group: a heading, an optional sentence, then its rows.
    ///
    /// The heading is `title` — 15pt semibold, primary. It used to be
    /// `sectionLabel`, which is 11pt tertiary: the same size and nearly the
    /// same grey as the caption beneath it, so "Timing" and the sentence
    /// explaining Timing carried identical weight and neither read as a
    /// heading. Three steps now — 15 semibold, 13 regular, 11 tertiary.
    @ViewBuilder
    private func settingsSection(
        _ title: String, _ caption: String?, @ViewBuilder _ content: () -> some View
    ) -> some View {
        GridRow {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title)
                    .font(Theme.Typography.title)
                    .foregroundStyle(Theme.Colors.textPrimary)
                    .accessibilityAddTraits(.isHeader)
                if let caption {
                    Text(caption)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textTertiary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .gridCellColumns(2)
            // More space above a heading than below it, so a section reads as
            // belonging to the rows under it rather than floating between two.
            .padding(.top, Theme.Space.xs)
            .padding(.bottom, Theme.Space.xxs)
        }

        content()
    }

    // MARK: - Timing

    private var timingSection: some View {
        settingsSection("Timing", "How long the Mac stays awake around a job.") {
            settingsRow("Wake it this early") {
                DurationPicker(
                    presets: [30, 60, 90, 120, 300].map { WGDuration(seconds: $0) },
                    current: store.config?.wakeBuffer ?? .zero
                ) { apply("wake_buffer", $0.wireString) }
            }
            .help(
                "How far ahead of a job WakeGuard wakes the Mac, so it is fully up "
                    + "rather than still mounting disks when the job fires."
            )

            settingsRow("Stay awake at least") {
                DurationPicker(
                    presets: [60, 120, 300, 600, 1800].map { WGDuration(seconds: $0) },
                    current: store.config?.defaultCeiling ?? .zero
                ) { apply("default_ceiling", $0.wireString) }
            }
            .help(
                "How long to hold sleep off for a job with no history yet. Once a job "
                    + "has run a few times this is replaced by a learned value."
            )

            settingsRow("Stay awake for unwatched") {
                DurationPicker(
                    presets: [60, 120, 180, 300, 600].map { WGDuration(seconds: $0) },
                    current: store.config?.wakeOnlyHold ?? .zero
                ) { apply("wake_only_hold", $0.wireString) }
            }
            .help(
                "Wake-only jobs cannot be observed, so their window is a fixed length "
                    + "rather than a learned one. Most adopted jobs use this, which makes it "
                    + "the setting that decides what they cost."
            )
        }
    }

    // MARK: - Automatic adoption

    private var adoptionSection: some View {
        settingsSection("Finding jobs", "WakeGuard can pick up jobs other apps create, so you don\u{2019}t have to add them.") {
            unlabelledRow {
                Toggle(
                    "Pick up new jobs on their own",
                isOn: Binding(
                        get: { store.config?.isAutoAdoptEnabled ?? false },
                        set: { setAutoAdopt(enabled: $0) }
                    )
                )
                .disabled(store.config == nil || (!canEnableAutoAdopt && !isAutoAdoptOn))
                .help(
                    "WakeGuard watches schedulers that run their own jobs and registers new "
                        + "ones as they appear. Crontab and launchd entries are never adopted "
                        + "automatically — covering those means editing a command line, which "
                        + "is your decision. Use `wakeguard import` for them."
                )
            }

            settingsRow("Status") {
                HStack(spacing: Theme.Space.sm) {
                    Button("Check Now") {
                        store.performReporting { try await $0.sync().summary }
                    }
                    .controlSize(.small)
                    .disabled(store.isPerformingAction)
                    Text(adoptionStateText)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }

            // The one message in this pane that must not be a tooltip: it says
            // the control above cannot be switched back on from here.
            if !canEnableAutoAdopt, !isAutoAdoptOn {
                unlabelledRow {
                    Label(
                        "No watchable scheduler is visible. Re-enable with "
                            + "`wakeguard config set auto_adopt <source>`.",
                        systemImage: Theme.Icon.warning
                    )
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.warning)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    private var isAutoAdoptOn: Bool { store.config?.isAutoAdoptEnabled ?? false }

    /// Sources this app can name when switching adoption back on.
    ///
    /// The daemon accepts only an explicit comma-separated list or "off" — there
    /// is no string that restores the unconfigured "watch everything adoptable"
    /// state, and no op that reports which sources those are. So the best the
    /// app can do is re-offer the sources it can currently see on registered
    /// jobs, excluding the ones the daemon refuses to watch.
    private var knownAdoptableSources: [String] {
        let excluded: Set<String> = ["crontab", "launchd", "systemd", "manual", ""]
        var seen: [String] = []
        for view in store.jobs {
            let source = view.job.source.lowercased()
            if !excluded.contains(source), !seen.contains(source) {
                seen.append(source)
            }
        }
        return seen
    }

    /// Union of what config already names and what the job list reveals.
    private var enableableSources: [String] {
        var sources = store.config?.watchedSources ?? []
        for source in knownAdoptableSources where !sources.contains(source) {
            sources.append(source)
        }
        return sources
    }

    private var canEnableAutoAdopt: Bool { !enableableSources.isEmpty }

    private var adoptionStateText: String {
        guard let config = store.config else { return "" }
        if !config.isAutoAdoptEnabled { return "off" }
        if let watched = config.watchedSources {
            return "watching \(watched.joined(separator: ", "))"
        }
        return "watching everything adoptable"
    }

    private func setAutoAdopt(enabled: Bool) {
        if enabled {
            let sources = enableableSources
            guard !sources.isEmpty else { return }
            apply("auto_adopt", sources.joined(separator: ","))
        } else {
            // The daemon maps "off" to an explicit empty list, which it then
            // respects. An empty string would do the same, but "off" is what
            // the CLI documents, so an error message quoting it will match.
            apply("auto_adopt", "off")
        }
    }

    // MARK: - Safety

    private var safetySection: some View {
        settingsSection("Safety", "With the lid shut, WakeGuard gives up and lets the Mac sleep rather than cook it or flatten the battery.") {
            sliderRow(
                title: "Let it sleep if hotter than",
                value: $thermalCutout,
                range: Self.thermalRange,
                format: { String(format: "%.0f°C", $0) },
                help: "Above this CPU temperature with the lid closed, every hold is "
                    + "force-released. Holding a bagged laptop awake without this is unsafe."
            ) { apply("thermal_cutout_c", String(Int(thermalCutout))) }

            sliderRow(
                title: "Let it sleep if battery under",
                value: $lowBatteryCutout,
                range: Self.batteryRange,
                format: { "\(Int($0))%" },
                help: "Below this charge on battery with the lid closed, holds are released "
                    + "so normal sleep takes over with charge to spare."
            ) { apply("low_battery_cutout_pct", String(Int(lowBatteryCutout))) }

            // A consequence of the slider above, not a setting of its own.
            //
            // It was a row with a label and a bare number where every other row
            // has a control — which reads as a slider that failed to render.
            // It is the same threshold restated with the rearm margin added, so
            // it belongs under the control that governs it, in caption grey,
            // where nobody will try to drag it.
            if let floor = derivedWakeFloor {
                unlabelledRow {
                    Text("Won\u{2019}t wake at all under \(floor)")
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textTertiary)
                        .help(
                            "The cutout plus the rearm margin. Waking the Mac at exactly the "
                                + "release threshold would spend charge and then immediately "
                                + "give up."
                        )
                }
            }
        }
    }

    private func sliderRow(
        title: String,
        value: Binding<Double>,
        range: ClosedRange<Double>,
        format: @escaping (Double) -> String,
        help: String,
        commit: @escaping () -> Void
    ) -> some View {
        settingsRow(title) {
            HStack(spacing: Theme.Space.sm) {
                // No tick marks, and no `step:`.
                //
                // Passing a step makes AppKit draw a tick for every stop, which
                // at 1°C over a 25° range is 26 dots under the track — read as
                // texture, not as scale, and the two sliders stacked together
                // looked like hatching. Apple's own guidance is that ticks are
                // for when someone needs to understand the scale or land on a
                // specific value; these are thresholds nobody sets precisely,
                // and the exact number is already shown beside the thumb.
                //
                // Whole numbers still reach the daemon: the binding rounds, so
                // the value is integral without the track advertising it.
                Slider(value: value.rounded, in: range) { editing in
                    if !editing { commit() }
                }
                .frame(maxWidth: .infinity)

                Text(format(value.wrappedValue))
                    .font(Theme.Typography.tabularSmall)
                    .foregroundStyle(Theme.Colors.textPrimary)
                    // Beside the thumb, not at the window's edge. It was pinned
                    // far right with the whole track between it and its label,
                    // so reading "what is this set to" meant crossing the pane.
                    // Fixed width so the track does not shift as digits change.
                    .frame(width: 46, alignment: .trailing)
                    .monospacedDigit()
            }
        }
        .help(help)
    }

    private var derivedWakeFloor: String? {
        guard let config = store.config, config.cutoutRearmMarginPct > 0 else { return nil }
        return "\(config.wakeFloorPct)%"
    }

    // MARK: - Alerts

    private var alertsSection: some View {
        settingsSection("Alerts", nil) {
            unlabelledRow {
                Toggle(
                    "Tell me when a job gets missed",
                    isOn: Binding(
                        get: { store.config?.notifyOnMissedJob ?? false },
                        set: { apply("notify_on_missed_job", $0 ? "true" : "false") }
                    )
                )
                .help("A macOS notification when a job WakeGuard was watching did not run.")
            }

            unlabelledRow {
                Toggle(
                    "Turn the Mac on if it\u{2019}s shut down",
                    isOn: Binding(
                        get: { store.config?.useWakeOrPowerOn ?? false },
                        set: { apply("use_wake_or_power_on", $0 ? "true" : "false") }
                    )
                )
                .help(
                    "Off by default: silently powering on a Mac someone deliberately shut "
                        + "down is a surprise most people would not consent to."
                )
            }
        }
    }

    // MARK: - Advanced

    /// Collapsed by default, and holding exactly one thing.
    ///
    /// That one thing is the webhook, which was previously a bare "Webhook URL"
    /// field sitting in Notifications between two checkboxes. To anyone who does
    /// not already run a service that accepts webhooks — which is nearly
    /// everyone — it is an unexplained blank asking for a URL they do not have,
    /// and an unanswerable question in a settings pane reads as something the
    /// user has failed to configure. It is a developer feature, so it lives
    /// behind a disclosure and says what it is for.
    private var advancedSection: some View {
        Group {
            DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text("Post problems to a URL")
                        .font(Theme.Typography.body)
                    Text(
                        "For piping alerts into Slack, a pager, or your own service. "
                            + "Overruns, wake failures, undetected jobs and cutouts are sent "
                            + "as JSON. Leave empty if you do not have one."
                    )
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textTertiary)
                    .fixedSize(horizontal: false, vertical: true)

                    TextField("", text: $webhookText, prompt: Text("https://…"))
                        .textFieldStyle(.roundedBorder)
                        .onSubmit { apply("webhook_url", webhookText) }
                        .help("Press Return to apply.")
                }
                .padding(.vertical, Theme.Space.xxs)
            }
        }
    }

    private var warningsSection: some View {
        settingsSection("Values that were adjusted", "These were outside the allowed range, so WakeGuard changed them.") {
            ForEach(store.configWarnings, id: \.self) { warning in
                Label(Format.noWidow(warning), systemImage: Theme.Icon.warning)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.warning)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    // MARK: - Status bar

    /// What was a "Connection" section of eight label/value rows — protocol
    /// version, status schema, socket path, sleep-blocked, last-updated.
    ///
    /// None of that is a setting, and none of it is actionable: it is diagnostic
    /// output, wanted perhaps twice in a product's life and by someone already
    /// being told to run `wakeguard doctor`. It was the largest section in the
    /// pane. It is now one line that answers the only question worth asking at a
    /// glance — is this thing connected — with the full dump a click away for
    /// the rare moment it matters.
    private var statusBar: some View {
        HStack(spacing: Theme.Space.sm) {
            Image(systemName: connectionIcon)
                .font(Theme.Typography.iconInline)
                .foregroundStyle(connectionTint)
                .accessibilityHidden(true)
            Text(connectionSummary)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
                .lineLimit(1)

            Spacer(minLength: Theme.Space.md)

            if let message = store.actionMessage {
                ActionMessageView(message: message)
                    .frame(maxWidth: 220)
            }

            Button("Copy Diagnostics") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(diagnostics, forType: .string)
                store.note("Diagnostics copied.")
            }
            .controlSize(.small)
            .help("Copies background service and helper versions, the socket path, and protocol "
                + "versions — the details a bug report needs.")
        }
        .padding(.horizontal, Theme.Space.md)
        .padding(.vertical, Theme.Space.sm)
        .background(.thinMaterial)
        .overlay(alignment: .top) { ThemeHairline() }
    }

    private var connectionSummary: String {
        switch store.connection {
        case .connected:
            let helper = (store.status?.helperConnected ?? false)
                ? "helper connected"
                : "helper not connected"
            return "Connected · \(helper)"
        case .connecting: return "Connecting…"
        case .unreachable: return "WakeGuard isn't running"
        case .mismatch: return "Version mismatch"
        case .failing: return "Connection error"
        }
    }

    private var connectionIcon: String {
        switch store.connection {
        case .connected:
            (store.status?.helperConnected ?? false) ? Theme.Icon.ok : Theme.Icon.warning
        case .connecting: Theme.Icon.info
        case .unreachable: Theme.Icon.disconnected
        case .mismatch, .failing: Theme.Icon.warning
        }
    }

    private var connectionTint: Color {
        switch store.connection {
        case .connected:
            (store.status?.helperConnected ?? false) ? Theme.Colors.ok : Theme.Colors.warning
        case .connecting: Theme.Colors.textSecondary
        case .unreachable: Theme.Colors.textSecondary
        case .mismatch, .failing: Theme.Colors.warning
        }
    }

    private var diagnostics: String {
        var lines = [
            "wakeguard diagnostics",
            "background service: \(connectionSummary)",
            "background service version: \(Format.orEmpty(store.status?.daemonVersion))",
            "helper: \(helperValue)",
            "sleep blocked: \((store.status?.sleepBlocked ?? false) ? "yes" : "no")",
            "protocol: \(WGProtocol.version)",
            "socket: \(WGProtocol.socketPath)",
        ]
        if let status = store.status {
            lines.append("status schema: \(status.schemaVersion)")
        }
        if let updated = store.lastUpdated {
            lines.append("last updated: \(Format.ago(updated, from: store.now))")
        }
        return lines.joined(separator: "\n")
    }

    private var helperValue: String {
        guard let status = store.status else { return Format.empty }
        guard status.helperConnected else { return "not connected" }
        return status.helperVersion.isEmpty ? "connected" : "connected (\(status.helperVersion))"
    }

    // MARK: - Applying

    /// Repopulates the editable fields from the daemon's config.
    ///
    /// Safe to call at any time: nothing here is wired to an `onChange` that
    /// writes back, so loading can never be mistaken for an edit. The webhook
    /// commits on Return and the sliders on release, both of which require the
    /// user. The pickers write on selection, which is also unambiguous.
    private func loadFields() {
        guard let config = store.config else { return }
        webhookText = config.webhookURL
        // A webhook that is already set should be visible rather than hidden
        // behind a collapsed disclosure, or it looks like it was lost.
        if !config.webhookURL.isEmpty { showAdvanced = true }
        thermalCutout = Self.thermalRange.contains(config.thermalCutoutC)
            ? config.thermalCutoutC
            : Self.thermalRange.lowerBound
        let battery = Double(config.lowBatteryCutoutPct)
        lowBatteryCutout = Self.batteryRange.contains(battery)
            ? battery
            : Self.batteryRange.lowerBound
    }

    private func apply(_ key: String, _ value: String) {
        store.perform("Saved.") { client in
            try await client.setConfig(key: key, value: value)
        }
        // Re-read so a clamped value is reflected rather than the chosen one.
        Task { @MainActor in
            await store.loadConfig()
            loadFields()
        }
    }
}

extension Binding where Value == Double {
    /// Rounds on the way in, so a continuous slider still yields whole numbers.
    ///
    /// The alternative is `Slider(value:in:step:)`, which quantises correctly
    /// but also draws a tick for every stop. This keeps the integer values and
    /// loses the hatching.
    var rounded: Binding<Double> {
        Binding<Double>(
            get: { wrappedValue },
            set: { wrappedValue = $0.rounded() }
        )
    }
}

/// Keeps the window exactly as tall as the view inside it.
///
/// `NSHostingController.sizingOptions = .preferredContentSize` would do this,
/// but `WindowCoordinator` documents why it is avoided: AppKit updates it
/// during a constraints pass and it can recurse. Measuring the content and
/// setting the frame once per change stays out of that pass.
private struct FitsWindowHeight: ViewModifier {
    @State private var window: NSWindow?

    func body(content: Content) -> some View {
        content
            .background {
                GeometryReader { proxy in
                    Color.clear
                        .onChange(of: proxy.size.height, initial: true) { _, height in
                            resize(to: height)
                        }
                }
            }
            .background(WindowReader { window = $0 })
    }

    private func resize(to height: CGFloat) {
        guard let window, height > 0 else { return }
        var frame = window.frame
        let target = height + (frame.height - window.contentLayoutRect.height)
        guard abs(target - frame.height) > 0.5 else { return }
        // Grow downward from the title bar rather than from the bottom edge, so
        // the window does not appear to jump when a disclosure opens.
        frame.origin.y += frame.height - target
        frame.size.height = target
        window.setFrame(frame, display: true, animate: false)
    }
}

/// Hands back the `NSWindow` hosting this view, once it has one.
private struct WindowReader: NSViewRepresentable {
    let onResolve: (NSWindow) -> Void

    func makeNSView(context _: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async {
            if let window = view.window { onResolve(window) }
        }
        return view
    }

    func updateNSView(_ view: NSView, context _: Context) {
        if let window = view.window { onResolve(window) }
    }
}
