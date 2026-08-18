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
/// every section: why wake-only jobs get a fixed window, why powering on a
/// shut-down Mac is opt-in, what a duration string may contain. All of it was
/// true and none of it belonged here. A settings pane is read by someone who
/// has already decided to change something; prose between them and the control
/// is an obstacle, and a wall of it makes the few genuinely important warnings
/// impossible to pick out. The reasoning now lives in tooltips, where it is
/// asked for, and in `Docs/ARCHITECTURE.md`, where it belongs.
struct SettingsWindowView: View {
    @Bindable var store: StatusStore

    /// Non-zero while the user is holding a control or a write is in flight.
    /// Polls do not overwrite the fields during that.
    @State private var editing = 0
    /// Which text field has the caret, if any.
    ///
    /// Owned by the pane rather than by each field, because "click anywhere
    /// else to finish typing" is a decision about the whole pane. With the
    /// focus held privately inside the field, there was nothing else for the
    /// caret to move to and clicking away left it stuck in the box.
    @FocusState private var focus: FieldFocus?
    // Seeded with what config.Default() ships, not with round numbers.
    //
    // These are placeholders for the instant before `loadFields()` runs, and
    // the battery one was 20 against a shipped default of 10, so anyone opening
    // Settings saw a figure that was wrong twice over: not their setting, and
    // not the default either. It also put 20 into every rendered screenshot,
    // because the offscreen renderer captures before `.task` fires.
    // TestSettingsPlaceholdersMatchDefaults keeps these in step.
    @State private var thermalCutout: Double = 80
    @State private var lowBatteryCutout: Double = 10
    @State private var webhookText = ""
    /// Persisted, like the popover's jobs disclosure. Someone who opens this
    /// once is usually coming back to it, and a disclosure that forgets makes
    /// them find it again every launch. It also makes the expanded pane
    /// renderable, which is how the layout crash below was reproduced.

    /// Which group of settings is on screen.
    ///
    /// The pane used to be one column of every setting goguma has, and it
    /// had outgrown the screen: 1266pt tall with Advanced closed, against
    /// roughly 1000pt of laptop, so its bottom could not be reached at all.
    /// The note where that layout was chosen said not fitting would be a
    /// signal there were too many settings rather than a reason to add a
    /// scrollbar. There are not too many settings. There are too many at
    /// once.
    ///
    /// Tabs rather than a scroll view, because a scrollbar hides what
    /// exists behind an interaction, and because a fixed height also
    /// removes the measure-then-resize the window did on every open, which
    /// is what made Settings seem slow to appear and then jump once it had.
    @AppStorage("settings.tab") private var tabRaw = SettingsTab.timing.rawValue

    var tab: SettingsTab { SettingsTab(rawValue: tabRaw) ?? .timing }

    /// Honoured for the Advanced disclosure. See `Theme.motion(reduced:)`.
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

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
                // background and its own scroll view, so the pane looked like a
                // different application from every other window, and a settings
                // list short enough to fit scrolled anyway. Laying it out
                // directly keeps the surface goguma uses everywhere else. The
                // note here used to say that a pane which stopped fitting meant
                // there were too many settings rather than too few scrollbars.
                // It stopped fitting, at 1266pt against a 1000pt laptop, and
                // both readings were wrong: the settings are fine, showing all
                // of them at once was not. Hence the tabs.
                // One `Grid` for the whole pane, not a stack per section.
                //
                // Mac settings put labels in a right-aligned column against a
                // left-aligned column of controls, and the column is as wide as
                // its longest label, no wider. This was a fixed 168pt guess
                // shared by hand between three row types, which is why "Let it
                // sleep if battery under" ran to the edge of its column while
                // "Status" left most of one empty, and why every control sat in
                // a narrow band with a third of the window blank to its right.
                //
                // A single grid measures the column once from the real strings,
                // so the sections align with each other rather than each being
                // internally consistent and mutually wrong.
                tabBar

                Grid(
                    alignment: .leadingFirstTextBaseline,
                    horizontalSpacing: Theme.Space.sm,
                    // sm, not xs. Four points is right between two lines of one control,
                    // and wrong for a column of separate decisions: three checkboxes at
                    // that spacing read as one block of text with boxes in it rather than
                    // as three things you can each answer.
                    verticalSpacing: Theme.Space.sm
                ) {
                    switch tab {
                    case .timing: timingSection
                    case .jobs: adoptionSection
                    case .safety: safetySection
                    case .alerts:
                        alertsSection
                        sectionRule
                        updatesSection
                    case .advanced: advancedSection
                    }
                    // Shown on every tab. A setting goguma had to correct is not a fact
                    // about the group it happens to sit in, and putting it behind the
                    // right tab would make the one message that says something went
                    // wrong the one message you have to go looking for.
                    if !store.configWarnings.isEmpty { warningsSection }
                }
                // Config writes no longer take this path.
                //
                // This was here because `perform` drops a second call made
                // while the first is in flight, so a fast second toggle was
                // silently discarded and then contradicted by the refresh.
                // Disabling the pane prevented that by making the whole window
                // grey out and come back on every slider release, a full-page
                // flash to guard one row. `store.writeConfig` is sequential
                // and never dropped, so nothing needs to be frozen.
                // Clicking the pane finishes typing.
                //
                // A text field on macOS keeps first responder until something
                // else takes it, and the rest of this pane is sliders,
                // checkboxes and pop-ups, none of which accept the caret. So
                // clicking away from the box left it focused with the caret
                // still blinking in it, and there was no way out but Return.
                // This is the surface the click lands on when it misses a
                // control, which is exactly when the caret should be given up.
                .contentShape(.rect)
                // Simultaneous, so it can never win a click a control wanted.
                // A plain `onTapGesture` here competes with the sliders and
                // checkboxes underneath it; this runs alongside them, which
                // also means clicking any other control finishes the edit,
                // which is what clicking another control means.
                .simultaneousGesture(TapGesture().onEnded { focus = nil })
                .padding(.horizontal, Theme.Space.md)
                .padding(.top, Theme.Space.sm)
                // A collapsed disclosure is its own bottom margin: it carries
                // the padding that makes it a hit target, and adding a full
                // pane margin under that is what made the gap conspicuous.
                .padding(.bottom, Theme.Space.md)
                .frame(maxWidth: .infinity, alignment: .leading)
            }

            statusBar
        }
        .frame(width: Theme.Surface.settingsWidth)
        // Height comes from the content, not a constant.
        //
        // It was a fixed 580pt, which is what the pane needs with **Advanced**
        // open. Closed (which is how it always opens) that left ~100pt of
        // empty surface above the status bar, and a window with a band of
        // nothing at the bottom reads as one that failed to load. Sizing to the
        // content means the pane fits either state exactly, and opening the
        // disclosure grows the window rather than revealing space that was
        // always there.
        .modifier(FitsWindowHeight())
        // Paint whatever height we are actually given, even if it exceeds the
        // content. The window is sized from the measurement above, so normally
        // the two agree, but when a host asks for more (the offscreen renderer
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
    /// Right-aligned labels are the Mac convention and they are correct, for a
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
                // `rowLabel`, not `body`: the same 13pt medium the Jobs list
                // uses for a job name. Both windows are lists of named things
                // with a control beside each, and at the same size in two
                // different weights they read as two different apps.
                .font(Theme.Typography.rowLabel)
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
    /// The heading is `title`: 15pt semibold, primary. It used to be
    /// `sectionLabel`, which is 11pt tertiary: the same size and nearly the
    /// same grey as the caption beneath it, so "Timing" and the sentence
    /// explaining Timing carried identical weight and neither read as a
    /// heading. Three steps now: 15 semibold, 13 regular, 11 tertiary.
    @ViewBuilder
    private func settingsSection(
        _ title: String, _ caption: String?, @ViewBuilder _ content: () -> some View
    ) -> some View {
        GridRow {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                // The heading is dropped when the tab above already carries it.
                //
                // "Timing" as a title directly under a tab labelled "Timing" is
                // the same word twice in 40pt of vertical space, and it cost the
                // pane a line of height on every tab to say nothing. The caption
                // stays: that is the part which says what the group is for.
                if title.lowercased() != tab.title.lowercased() {
                    Text(title)
                        .font(Theme.Typography.title)
                        .foregroundStyle(Theme.Colors.textPrimary)
                        .accessibilityAddTraits(.isHeader)
                }
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
        settingsSection("Timing", "When the Mac wakes up for a job, and how long it stays up.") {
            // Labels a person can act on without knowing how goguma works.
            //
            // These were "Wake it this early", "Stay awake at least" and "Stay
            // awake for unwatched". The first is missing what it is early
            // *for*, the second never says at least *what*, and the third
            // turns on "unwatched", which is a word this app invented for a
            // job whose process it cannot recognise. Each one now names the
            // job it applies to, because that is the only thing separating
            // three otherwise identical durations.
            settingsRow("Wake up before a job") {
                DurationPicker(
                    presets: [30, 60, 90, 120, 300].map { WGDuration(seconds: $0) },
                    current: store.config?.wakeBuffer ?? .zero
                ) { apply("wake_buffer", $0.wireString) }
            }
            .help(
                "How far ahead of a job goguma wakes the Mac, so it is fully up "
                    + "rather than still mounting disks when the job fires."
            )

            settingsRow("Stay awake for a new job") {
                DurationPicker(
                    presets: [60, 120, 300, 600, 1800].map { WGDuration(seconds: $0) },
                    current: store.config?.defaultCeiling ?? .zero
                ) { apply("default_ceiling", $0.wireString) }
            }
            .help(
                "How long to stay awake for a job goguma hasn't timed yet. After a "
                    + "few runs it knows how long that job takes and uses that instead."
            )

            settingsRow("Stay awake for a job it can't watch") {
                DurationPicker(
                    presets: [60, 120, 180, 300, 600].map { WGDuration(seconds: $0) },
                    current: store.config?.wakeOnlyHold ?? .zero
                ) { apply("wake_only_hold", $0.wireString) }
            }
            .help(
                "Some jobs run in a way goguma can't recognise, so it can't tell when "
                    + "they finish and stays awake for a fixed stretch instead. Most jobs "
                    + "it finds by itself are like this, so this is what they cost."
            )

            // Here rather than under Safety, because this section's own caption
            // promises "how long it stays up", and this is the half of that
            // sentence about afterwards.
            unlabelledRow {
                Toggle(
                    "Put it back to sleep afterwards",
                    isOn: Binding(
                        get: { store.config?.sleepAfterWake ?? true },
                        set: { apply("sleep_after_wake", $0 ? "on" : "off") }
                    )
                )
                .disabled(store.config == nil)
                .help(
                    "macOS treats a scheduled wake as though you had opened the lid, so "
                        + "everything else on the Mac wakes up too and can keep it up for "
                        + "hours after a job that took thirty seconds. goguma only does this "
                        + "for wakes it caused itself, once every job is finished and nobody "
                        + "has touched the keyboard for two minutes."
                )
            }

            // Beside "put it back to sleep afterwards", because both are about
            // what goguma does with the machine when nobody asked it to.
            unlabelledRow {
                Toggle(
                    "Keep coding agents running with the lid shut",
                    isOn: Binding(
                        get: { store.config?.agentHooks ?? true },
                        set: { apply("agent_hooks", $0 ? "on" : "off") }
                    )
                )
                .disabled(store.config == nil)
                .help(
                    "An agent cannot be recognised from outside: its process is there "
                        + "whether or not it is working, so it has to say so. This adds one "
                        + "line to Claude Code, Codex and Cursor's own settings, beside "
                        + "whatever is already there, and the machine stays awake until the "
                        + "agent stops rather than for a fixed time. The limits on the "
                        + "Safety tab still apply: it sleeps anyway if the Mac gets too hot "
                        + "or the battery runs low. Turning this off takes that line back "
                        + "out, and an agent stops when the lid closes."
                )
            }
        }
    }

    // MARK: - Automatic adoption

    private var adoptionSection: some View {
        settingsSection(
            "Finding jobs",
            // Names only what there is a reader for.
            //
            // This said "Claude, ChatGPT, hermes, crontab and launchd". There
            // are three providers: crontab, launchd and hermes. Claude and
            // ChatGPT have no reader, so the sentence was true only if those
            // apps happen to schedule through launchd, and false if they
            // schedule inside themselves. Naming a source goguma cannot read
            // is the one claim a settings pane must not make.
            //
            // The second sentence is the way out: any app can be added with
            // `goguma scheduler add`, so the honest version says what is
            // covered and how to cover the rest.
            "goguma finds jobs from crontab, launchd and hermes by itself. "
                + "Other apps can be added with the goguma scheduler command."
        ) {
            unlabelledRow {
                Toggle(
                    "Pick up new jobs on their own",
                isOn: Binding(
                        get: { store.config?.isAutoAdoptEnabled ?? false },
                        set: { setAutoAdopt(enabled: $0) }
                    )
                )
                .disabled(store.config == nil)
                .help(
                    "goguma watches every scheduler on this Mac and registers new jobs as "
                        + "they appear, including crontab and launchd. It never edits your "
                        + "crontab to do it, so a job whose process it can't recognise gets "
                        + "a fixed window rather than exact timing."
                )
            }

            // A sentence and a button, spanning the pane.
            //
            // This was a labelled row, and the label column is sized by "Stay
            // awake for unwatched" while the label here is one short word. That
            // left "Status" alone against a wide gap with its own value marooned
            // on the far side of it, which is the spacing that read as broken.
            // Nothing about this is a label-and-control pair, so it stops
            // pretending to be one.
            unlabelledRow {
                HStack(spacing: Theme.Space.sm) {
                    Text(adoptionStateText)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                    // Beside the sentence it belongs to, not pinned to the
                    // far edge with the width of the pane between them.
                    Button("Update now") {
                        store.performReporting { try await $0.sync().summary }
                    }
                    .controlSize(.small)
                    .disabled(store.isPerformingAction)
                    Spacer(minLength: 0)
                }
            }

        }
    }

    private var isAutoAdoptOn: Bool { store.config?.isAutoAdoptEnabled ?? false }

    private var adoptionStateText: String {
        guard let config = store.config else { return "" }
        if !config.isAutoAdoptEnabled { return "Not looking for new jobs." }
        if let watched = config.watchedSources {
            return "Watching \(watched.joined(separator: ", "))."
        }
        return "Watching every scheduler on this Mac."
    }

    private func setAutoAdopt(enabled: Bool) {
        if enabled {
            // "all" restores the unconfigured "watch everything adoptable"
            // state; the daemon added it precisely so a UI toggle is not a
            // one-way door. Re-sending a scraped source list instead
            // permanently narrowed coverage to whatever happened to be
            // visible at the moment of the click.
            apply("auto_adopt", "all")
        } else {
            // The daemon maps "off" to an explicit empty list, which it then
            // respects. An empty string would do the same, but "off" is what
            // the CLI documents, so an error message quoting it will match.
            apply("auto_adopt", "off")
        }
    }

    // MARK: - Safety

    private var safetySection: some View {
        settingsSection(
            "Safety",
            "goguma lets the Mac sleep rather than let it overheat or run out of battery."
        ) {
            sliderRow(
                title: "Let it sleep if hotter than",
                value: $thermalCutout,
                range: Self.thermalRange,
                // 5°C. The range is 70 to 95, so the track shows six stops and
                // the values people actually pick are on them.
                step: 5,
                unit: "°C",
                focusID: .thermal,
                help: "Above this CPU temperature, every hold is released and the Mac "
                    + "sleeps normally, so a laptop in a bag doesn't overheat."
            ) { apply("thermal_cutout_c", String(Int(thermalCutout))) }

            sliderRow(
                title: "Let it sleep if battery under",
                value: $lowBatteryCutout,
                range: Self.batteryRange,
                // 5%. The range is 5 to 50, which is ten stops.
                step: 5,
                unit: "%",
                focusID: .battery,
                help: "Below this charge, holds are released and the Mac sleeps normally, "
                    + "so it doesn't run out of battery while you are away from it."
            ) { apply("low_battery_cutout_pct", String(Int(lowBatteryCutout))) }

            // A consequence of the slider above, not a setting of its own.
            //
            // It was a row with a label and a bare number where every other row
            // has a control, which reads as a slider that failed to render.
            // It is the same threshold restated with the rearm margin added, so
            // it belongs under the control that governs it, in caption grey,
            // where nobody will try to drag it.
        }
    }

    private func sliderRow(
        title: String,
        value: Binding<Double>,
        range: ClosedRange<Double>,
        step: Double,
        unit: String,
        focusID: FieldFocus,
        help: String,
        commit: @escaping () -> Void
    ) -> some View {
        settingsRow(title) {
            HStack(spacing: Theme.Space.sm) {
                // Snapped to `step`, with the ticks drawn.
                //
                // A 1-unit step over a 25-unit range draws 26 dots that read as
                // hatching rather than as a scale. The answer is a coarser
                // step: at 5 the track shows six stops, the thumb lands on
                // them, and the round numbers people actually choose stop
                // needing a steady hand. Anything between the stops is still
                // reachable by typing it into the field beside the track.
                Slider(value: value.rounded, in: range, step: step) { held in
                    // Counted, not just committed on release. A poll landing
                    // mid-drag used to reload the field under the cursor,
                    // which reset the thumb and cancelled the gesture.
                    if held {
                        editing += 1
                    } else {
                        editing -= 1
                        commit()
                    }
                }
                // Not `maxWidth: .infinity`. The track took every point the row
                // had, which pushed the number to the far edge of the window
                // with the whole slider between it and the thumb it belongs to.
                .frame(width: 200)

                ThresholdField(
                    value: value,
                    editing: $editing,
                    focus: $focus,
                    id: focusID,
                    range: range,
                    unit: unit,
                    commit: commit
                )

                Spacer(minLength: 0)
            }
        }
        .help(help)
    }

    // MARK: - Alerts

    /// Hearing about problems goguma cannot tell you about itself.
    ///
    /// A bug that only appears on hardware the author does not own reaches
    /// people who then have no way to find out: goguma quietly stops waking
    /// the Mac, and nothing says a fix exists.
    ///
    /// Built like Alerts and Finding jobs rather than as prose. It had a
    /// caption promising goguma "can tell you when a problem is found" above a
    /// row that offered no way to switch that on, because a build with no
    /// signing key cannot verify a notice and hides the control. A section
    /// whose description is of a thing the pane does not offer is worse than
    /// no section, and its caption-then-text rhythm did not match any other
    /// group here.
    private var updatesSection: some View {
        settingsSection("Staying up to date", nil) {
            // Only when this build can actually verify a notice.
            if store.advisoriesAvailable {
                unlabelledRow {
                    Toggle("Tell me when a problem is found", isOn: Binding(
                        get: { store.config?.advisoryChecks ?? false },
                        set: { apply("advisory_checks", $0 ? "on" : "off") }
                    ))
                    .toggleStyle(.checkbox)
                    .help("Fetches a small signed file from getgoguma.com once a day. It "
                        + "sends nothing about you or your jobs: no account, no identifier, "
                        + "not even the version you are running. What comes back can show a "
                        + "message and can't change any setting.")
                }
            }

            // The same shape as "Watching every scheduler on this Mac. [Update
            // now]" two sections up: a sentence, then the action beside it.
            unlabelledRow {
                HStack(spacing: Theme.Space.sm) {
                    Text(store.advisoriesAvailable
                        ? "Or by email, when there is something to say."
                        : "Get an email when something breaks or gets fixed.")
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                    Button("Sign up") {
                        if let url = URL(string: "https://getgoguma.com/updates") {
                            NSWorkspace.shared.open(url)
                        }
                    }
                    .controlSize(.small)
                    Spacer(minLength: 0)
                }
            }
        }
    }

    private var alertsSection: some View {
        settingsSection("Alerts", "What goguma tells you about, and how.") {
            unlabelledRow {
                Toggle(
                    "Tell me when a job gets missed",
                    isOn: Binding(
                        get: { store.config?.notifyOnMissedJob ?? false },
                        set: { apply("notify_on_missed_job", $0 ? "true" : "false") }
                    )
                )
                .help("A macOS notification when a job goguma was watching didn't run.")
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
                        + "down is a surprise most people wouldn't consent to. It also does "
                        + "nothing useful on a Mac with FileVault, which is most of them: "
                        + "the machine powers on, stops at the unlock screen, and the job is "
                        + "missed anyway. Waking a sleeping Mac is unaffected."
                )
            }
        }
    }

    // MARK: - Advanced

    /// Collapsed by default, and holding exactly one thing.
    ///
    /// That one thing is the webhook, which was previously a bare "Webhook URL"
    /// field sitting in Notifications between two checkboxes. To anyone who does
    /// not already run a service that accepts webhooks, which is nearly
    /// everyone; it is an unexplained blank asking for a URL they do not have,
    /// and an unanswerable question in a settings pane reads as something the
    /// user has failed to configure. It is a developer feature, so it lives
    /// behind a disclosure and says what it is for.
    @ViewBuilder
    private var advancedSection: some View {
        // A caption, like every other tab has.
        //
        // This was a disclosure row, from when Advanced was the last section of
        // one long column and had to be collapsible to keep the pane's height
        // down. It is a tab now: there is nothing to collapse it out of, and a
        // control that only ever hides the thing you navigated to is a control
        // with no correct use.
        GridRow {
            Text("Settings most people never need to change.")
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textTertiary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.bottom, Theme.Space.xxs)
                .gridCellColumns(2)
        }

        Group {
            GridRow {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text("Post problems to a URL")
                        .font(Theme.Typography.rowLabel)
                    Text(
                        "For piping alerts into Slack, a pager, or your own service. "
                            + "Overruns, wake failures, undetected jobs and cutouts are sent "
                            + "as JSON. Leave empty if you don't have one."
                    )
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textTertiary)
                    .fixedSize(horizontal: false, vertical: true)

                    TextField("", text: $webhookText, prompt: Text("https://…"))
                        .textFieldStyle(.roundedBorder)
                        .tint(Theme.Colors.accent)
                        .focused($focus, equals: .webhook)
                        .onSubmit {
                            apply("webhook_url", webhookText)
                            focus = nil
                        }
                        .help("Press Return to apply.")
                }
                .padding(.bottom, Theme.Space.xs)
                .gridCellColumns(2)
            }

            // The bounds on the learned ceiling, and the headroom above the
            // battery cutout.
            //
            // These were settable from `goguma config` and nowhere in the app,
            // so the two surfaces disagreed about what was configurable at all.
            // They are here rather than in Timing because they bound a value
            // goguma works out for itself, which is a different kind of
            // decision from "how early to wake".
            settingsRow("Never hold less than") {
                DurationPicker(
                    presets: [15, 30, 60, 120].map { WGDuration(seconds: $0) },
                    current: store.config?.minCeiling ?? .zero
                ) { apply("min_ceiling", $0.wireString) }
            }
            .help(
                "A floor under the learned window. A job measured faster than this still "
                    + "gets this much, so a brief stall doesn't end the hold early."
            )

            settingsRow("Never hold more than") {
                DurationPicker(
                    presets: [1800, 3600, 7200, 14400].map { WGDuration(seconds: $0) },
                    current: store.config?.maxCeiling ?? .zero
                ) { apply("max_ceiling", $0.wireString) }
            }
            .help(
                "The cap that ends a hung job's hold. However long a job has taken "
                    + "before, it never holds sleep off for longer than this."
            )

            settingsRow("Battery headroom") {
                PercentPicker(
                    presets: [1, 5, 10, 20],
                    current: store.config?.cutoutRearmMarginPct ?? 0
                ) { apply("cutout_rearm_margin_pct", String($0)) }
            }
            .help(
                "How far above the cutout the battery must recover before holds resume, "
                    + "so a machine sitting on the threshold doesn't release and re-take "
                    + "a hold repeatedly."
            )
        }
    }

    private var warningsSection: some View {
        settingsSection("Values that were adjusted", "These were outside the allowed range, so goguma changed them.") {
            ForEach(store.configWarnings, id: \.self) { warning in
                Label(Format.noWidow(warning), systemImage: Theme.Icon.warning)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.warning)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    // MARK: - Status bar

    /// What was a "Connection" section of eight label/value rows: protocol
    /// version, status schema, socket path, sleep-blocked, last-updated.
    ///
    /// None of that is a setting, and none of it is actionable: it is diagnostic
    /// output, wanted perhaps twice in a product's life and by someone already
    /// being told to run `goguma doctor`. It was the largest section in the
    /// pane. It is now one line that answers the only question worth asking at a
    /// glance (is this thing connected) with the full dump a click away for
    /// the rare moment it matters.
    /// Whether the app is talking to the daemon, and nothing else.
    ///
    /// It used to also carry a "Copy Diagnostics" button and report the helper
    /// separately. Both were built for filing a bug rather than for using the
    /// app: the helper is an implementation detail with no action attached, and
    /// a copy button for socket paths and protocol versions is a developer tool
    /// sitting permanently in a settings pane. Between them they wedged the
    /// transient action message into whatever width was left, which is what
    /// made the row read as unevenly spaced. `goguma doctor` prints all of it.
    private var statusBar: some View {
        HStack(spacing: Theme.Space.xs) {
            Image(systemName: connectionIcon)
                .font(Theme.Typography.iconInline)
                .foregroundStyle(connectionTint)
                .accessibilityHidden(true)
            Text(connectionSummary)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
                .lineLimit(1)

            Spacer(minLength: Theme.Space.sm)

            if let message = store.actionMessage {
                ActionMessageView(message: message)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, Theme.Space.md)
        .padding(.vertical, Theme.Space.sm)
        .background(.thinMaterial)
        .overlay(alignment: .top) { ThemeHairline() }
    }

    private var connectionSummary: String {
        switch store.connection {
        case .connected: return "Connected"
        case .connecting: return "Connecting…"
        case .unreachable: return "goguma isn't running"
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

    // MARK: - Applying

    /// Repopulates the editable fields from the daemon's config.
    ///
    /// Safe to call at any time: nothing here is wired to an `onChange` that
    /// writes back, so loading can never be mistaken for an edit. The webhook
    /// commits on Return and the sliders on release, both of which require the
    /// user. The pickers write on selection, which is also unambiguous.
    private func loadFields() {
        // Not while the user is in the middle of something.
        //
        // The pane re-reads config on every poll, and the poll landing during
        // a drag reset the slider under the cursor and cancelled the gesture,
        // which is most of why it was hard to move at all.
        guard editing == 0, let config = store.config else { return }
        webhookText = config.webhookURL
        // A webhook that is already set should be visible rather than hidden
        // behind a collapsed disclosure, or it looks like it was lost.
        thermalCutout = Self.thermalRange.contains(config.thermalCutoutC)
            ? config.thermalCutoutC
            : Self.thermalRange.lowerBound
        let battery = Double(config.lowBatteryCutoutPct)
        lowBatteryCutout = Self.batteryRange.contains(battery)
            ? battery
            : Self.batteryRange.lowerBound
    }

    private func apply(_ key: String, _ value: String) {
        editing += 1
        Task { @MainActor in
            // Write, then re-read, in that order and in one task. These were
            // two: a detached write and a second task that re-read. The read
            // raced the write and usually won, so a slider released on 25 was
            // written as 25 and redrawn at the value it had before, which
            // reads as the setting refusing to take.
            await store.writeConfig(key: key, value: value)
            editing -= 1
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

    /// A whole-number binding that refuses anything outside `range`.
    ///
    /// For the field beside a slider. Someone typing a temperature into a box
    /// can type 900, and the honest response is not an error dialog but the
    /// box showing what the setting is: the value clamps, and the field
    /// redraws with the number that was actually stored.
    func clamped(to range: ClosedRange<Double>) -> Binding<Double> {
        Binding<Double>(
            get: { wrappedValue },
            set: { wrappedValue = Swift.min(Swift.max($0.rounded(), range.lowerBound), range.upperBound) }
        )
    }
}

/// Which of the pane's text fields holds the caret.
enum FieldFocus: Hashable {
    case thermal
    case battery
    case webhook
}

/// The number beside a threshold slider, typed rather than dragged.
///
/// A slider says "roughly here", which is the right control for a temperature
/// nobody has an opinion about and the wrong one for someone who wants 82. The
/// field takes any value in the slider's range and turns down the rest by
/// snapping back to what is set, rather than by raising a complaint about it.
///
/// Drawn on the theme's own surface rather than with `.roundedBorder`. That
/// style paints an opaque system white which, on the arctic purple pane, was a
/// bright rectangle sitting in a window that has no other white in it.
private struct ThresholdField: View {
    @Binding var value: Double
    /// Held above zero while this field has focus, so a poll landing mid-type
    /// does not reload the number out from under the caret.
    @Binding var editing: Int
    var focus: FocusState<FieldFocus?>.Binding
    let id: FieldFocus
    let range: ClosedRange<Double>
    let unit: String
    let commit: () -> Void

    /// Fixed, and the same for every row.
    ///
    /// Both boxes were sized by their contents, so "80°C" and "5%" were
    /// different widths sitting at different distances from their tracks, and
    /// the number moved sideways as digits came and went. The number column
    /// fits the widest value either range can produce, and the pair is then
    /// centred in a box wide enough for the longer of the two units, so every
    /// row's box is identical and its contents sit in the middle of it.
    ///
    /// The unit is not given a column of its own. "°C" and "%" are genuinely
    /// different widths, and padding the narrower one out to match left "20 %"
    /// looking like it had drifted left inside its box.
    private static let numberWidth: CGFloat = 24
    private static let contentWidth: CGFloat = 46

    private var isFocused: Bool { focus.wrappedValue == id }

    var body: some View {
        // The unit sits inside the box with the number, not outside it.
        //
        // Outside, "80" and "°C" were two separate objects with a gap between
        // them, and the reader has to put them back together to get a
        // temperature. One box, one value.
        HStack(spacing: 1) {
            TextField(
                "",
                value: $value.clamped(to: range),
                format: .number.precision(.fractionLength(0))
            )
            .textFieldStyle(.plain)
            .multilineTextAlignment(.trailing)
            .frame(width: Self.numberWidth)
            // The caret and the selection, in the app's own colour.
            //
            // Untinted they are the system blue, which is the one hue this
            // theme never uses, so typing put a blinking blue bar in the
            // middle of a purple pane.
            .tint(Theme.Colors.accent)
            .focused(focus, equals: id)
            .onSubmit {
                commit()
                // Return finishes the edit and gives up the caret. Leaving it
                // in the box means the next Return does nothing visible.
                focus.wrappedValue = nil
            }

            Text(unit)
                .foregroundStyle(Theme.Colors.textSecondary)
        }
        .font(Theme.Typography.rowLabel)
        .monospacedDigit()
        .frame(width: Self.contentWidth)
        .onChange(of: isFocused) { _, nowFocused in
            // Clicking away is as much a "done" as pressing Return, and losing
            // the value because it was finished the wrong way reads as the
            // setting not working.
            if nowFocused {
                editing += 1
            } else {
                editing -= 1
                commit()
            }
        }
        .padding(.horizontal, Theme.Space.xs)
        .padding(.vertical, 3)
        .background(
            RoundedRectangle(cornerRadius: Theme.Radius.badge, style: .continuous)
                .fill(Theme.Colors.surface)
        )
        .overlay(
            RoundedRectangle(cornerRadius: Theme.Radius.badge, style: .continuous)
                .strokeBorder(
                    isFocused ? Theme.Colors.accent : Theme.Colors.divider,
                    lineWidth: Theme.Stroke.hairline
                )
        )
        .contentShape(.rect)
        // The whole box is the target, not just the glyphs. Clicking the unit
        // or the padding used to land on the pane behind it and do nothing.
        .onTapGesture { focus.wrappedValue = id }
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
    /// True while a deferred resize is in flight, so its own layout echo does
    /// not queue another one.
    @State private var resizing = false

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

    /// Remembers what a tab measured, so the next open starts at that size.
    ///
    /// The measurement can only happen once the pane has been laid out, and by
    /// then the window is on screen at whatever size it was opened with. A
    /// constant that fits the tallest tab therefore showed a band of empty
    /// surface under every shorter one for the frame or two before the resize
    /// landed. Storing the real number means the second open of a tab, and
    /// every one after it, starts at the height it is going to end at.
    static func remember(_ height: CGFloat, forTab tab: String) {
        UserDefaults.standard.set(Double(height), forKey: "settings.height.\(tab)")
    }

    /// The height a tab measured last time, if it ever has.
    static func rememberedHeight(forTab tab: String) -> CGFloat? {
        let h = UserDefaults.standard.double(forKey: "settings.height.\(tab)")
        // A stored zero means "never measured", and anything implausible is
        // not worth opening a window at.
        return h > 120 ? CGFloat(h) : nil
    }

    /// Resizes the window to fit the content, on the next runloop pass.
    ///
    /// Never synchronously. This is called from `onChange` of a
    /// `GeometryReader`'s height, which runs *inside* SwiftUI's layout pass, and
    /// `setFrame` there re-invalidates the constraints being computed.
    /// AppKit detects the re-entrant update and throws, which for a menu bar
    /// app means the whole process dies: opening the Advanced disclosure grew
    /// the pane enough to trigger it every time, and the app vanished from the
    /// menu bar with it.
    ///
    /// The `resizing` guard is for the second half of the loop. Even deferred,
    /// a resize changes the content height, which fires `onChange` again; the
    /// flag drops that echo rather than letting it settle over several frames.
    private func resize(to height: CGFloat) {
        guard let window, height > 0, !resizing else { return }
        // Recorded even when no resize follows, because the value is wanted for
        // the *next* open rather than for this one.
        Self.remember(height, forTab: UserDefaults.standard.string(forKey: "settings.tab") ?? "timing")
        resizing = true
        DispatchQueue.main.async {
            defer { resizing = false }
            var frame = window.frame
            let target = height + (frame.height - window.contentLayoutRect.height)
            guard target.isFinite, target > 0, abs(target - frame.height) > 0.5 else { return }
            // Grow downward from the title bar rather than from the bottom
            // edge, so the window does not appear to jump when a disclosure
            // opens.
            frame.origin.y += frame.height - target
            frame.size.height = target
            window.setFrame(frame, display: true, animate: false)
        }
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

/// The groups the settings pane is divided into.
///
/// Five, following the sections the pane already had rather than inventing a
/// new taxonomy: anyone who knew where a setting lived finds it in the same
/// company. "Staying up to date" joins Alerts, because both are goguma telling
/// you something rather than goguma doing something.
enum SettingsTab: String, CaseIterable, Identifiable {
    case timing, jobs, safety, alerts, advanced

    var id: String { rawValue }

    var title: String {
        switch self {
        case .timing: "Timing"
        case .jobs: "Jobs"
        case .safety: "Safety"
        case .alerts: "Alerts"
        case .advanced: "Advanced"
        }
    }

    var icon: String {
        switch self {
        case .timing: "clock"
        case .jobs: "list.bullet"
        case .safety: "shield"
        case .alerts: "bell"
        case .advanced: "gearshape.2"
        }
    }
}

extension SettingsWindowView {
    /// The row of groups across the top.
    ///
    /// Drawn rather than a `Picker(.segmented)`, for the same reason this pane
    /// is not a `Form`: the segmented control brings AppKit's own greys and
    /// corner radii, and one control in the system palette sitting on goguma's
    /// surface reads as a piece of a different application.
    @ViewBuilder
    var tabBar: some View {
        HStack(spacing: Theme.Space.xxs) {
            ForEach(SettingsTab.allCases) { t in
                Button { tabRaw = t.rawValue } label: {
                    SettingsTabLabel(tab: t, selected: tab == t)
                }
                .buttonStyle(.plain)
                .pointingHand()
                .accessibilityLabel(t.title)
                .accessibilityAddTraits(tab == t ? [.isSelected] : [])
            }
        }
        .padding(.bottom, Theme.Space.sm)
    }
}

/// One group's button.
///
/// Its own view because the whole bar in one expression is more than the Swift
/// type checker will finish: it gave up on it outright.
private struct SettingsTabLabel: View {
    let tab: SettingsTab
    let selected: Bool

    var body: some View {
        let fill: Color = selected ? Theme.Colors.cardFill : Color.clear
        let ink: Color = selected ? Theme.Colors.heading : Theme.Colors.textSecondary

        return HStack(spacing: Theme.Space.xxs) {
            Image(systemName: tab.icon)
                .font(Theme.Typography.caption)
            Text(tab.title)
                .font(Theme.Typography.rowLabel)
        }
        .padding(.horizontal, Theme.Space.sm)
        .padding(.vertical, Theme.Space.xs)
        .frame(maxWidth: .infinity)
        .background(
            RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).fill(fill)
        )
        .foregroundStyle(ink)
        .contentShape(.rect)
    }
}
