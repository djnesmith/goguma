import SwiftUI

/// Add / edit a job.
///
/// The pattern tester is the point of this sheet. Pattern detection fails
/// silently at 3am when the regexp doesn't match, so the sheet asks the daemon
/// to evaluate the pattern against the live process table while the user types;
/// a wrong pattern is visible here, not a month later in the history.
struct JobEditSheet: View {
    enum Mode: Identifiable, Hashable {
        case add
        case edit(Job)

        var id: String {
            switch self {
            case .add: "add"
            case let .edit(job): "edit-\(job.id)"
            }
        }

        var isEdit: Bool {
            if case .edit = self { return true }
            return false
        }
    }

    let store: StatusStore
    let mode: Mode
    var onSaved: () -> Void

    @Environment(\.dismiss) private var dismiss

    @State private var draft: Job
    @State private var maxRuntimeText: String
    @State private var wakeBufferText: String
    @State private var matchResult: MatchTestResponse?
    @State private var matchError: String?
    @State private var isTesting = false
    @State private var saveError: String?

    /// Nothing is focused when the sheet opens.
    ///
    /// AppKit hands first responder to the first text field, so Add Job
    /// arrived with a cursor blinking in Name before the user had decided
    /// anything. Declaring a focus target and leaving it nil gives SwiftUI
    /// something to honour instead.
    @FocusState private var focusedField: Field?

    private enum Field: Hashable { case name }

    // The alarm controls. The cron expression is derived from these on every
    // change and written into the draft; it is never edited directly.
    @State private var repeat_: ScheduleBuilder.Repeat = .daily
    @State private var timeOfDay = Calendar.current.date(
        bySettingHour: 9, minute: 0, second: 0, of: Date()
    ) ?? Date()
    @State private var weekdays: Set<Int> = [1, 2, 3, 4, 5]
    @State private var everyHours = 6

    @State private var isSaving = false
    @State private var testTask: Task<Void, Never>?

    init(store: StatusStore, mode: Mode, onSaved: @escaping () -> Void) {
        self.store = store
        self.mode = mode
        self.onSaved = onSaved
        switch mode {
        case .add:
            // Opens on a real schedule, not on "Custom…" with an empty field.
            // A new job starting in the state that means "I will write cron
            // myself" points the least confident user at the hardest option.
            var fresh = Job()
            fresh.schedule = "0 9 * * *"
            // Wake-only until the command says otherwise. It is the mode that
            // always works: it needs nothing from the user and cannot claim a
            // job is being watched when it is not. `deriveDetection` upgrades
            // it the moment there is a command distinctive enough to match.
            fresh.detection = .wakeOnly
            _draft = State(initialValue: fresh)
            _maxRuntimeText = State(initialValue: "")
            _wakeBufferText = State(initialValue: "")
        case let .edit(job):
            _draft = State(initialValue: job)
            _maxRuntimeText = State(initialValue: job.maxRuntime.isZero ? "" : job.maxRuntime.wireString)
            _wakeBufferText = State(initialValue: job.wakeBuffer.isZero ? "" : job.wakeBuffer.wireString)
            // Seed the alarm controls from the job's real schedule. Leaving
            // them at their defaults meant the sheet displayed a fabricated
            // "daily at 9:00" for a job firing at 18:00, and touching any
            // control rebuilt the expression from those defaults, silently
            // moving the fire time. Unrepresentable expressions keep the
            // defaults; they are never written back unless a control changes.
            if let seed = ScheduleBuilder.parse(job.schedule) {
                _repeat_ = State(initialValue: seed.repeating)
                _timeOfDay = State(initialValue: seed.time)
                _weekdays = State(initialValue: seed.weekdays)
                _everyHours = State(initialValue: seed.everyHours)
            }
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.md) {
            Text(mode.isEdit ? "Edit Job" : "Add Job")
                .font(Theme.Typography.title)
                .foregroundStyle(Theme.Colors.textPrimary)

            // The app's own row weight, not the system default.
            //
            // A `Form`'s field labels inherit the environment font, so without
            // this the sheet rendered its labels in a different weight from
            // every list and settings row in the app, same size, different
            // face, which reads as a different application.
            Form {
                Section {
                    TextField("Name", text: $draft.name)
                        .focused($focusedField, equals: .name)
                        .disabled(mode.isEdit)
                        .help(
                            mode.isEdit
                                ? "A job's name is its identity on disk and in IPC, so it can't be changed here."
                                : "Used as the job's id. Lower-cased and hyphenated."
                        )

                    // An alarm, not an expression.
                    //
                    // "Custom…" used to reveal a cron field, which is the
                    // thing the presets existed to avoid: anyone who fell off
                    // the preset list landed on `0 9 * * *` with no way to
                    // reason about it. These are the controls a person already
                    // knows from setting an alarm: how often, at what time, on
                    // which days. The cron expression is generated from them
                    // and never shown.
                    Picker("Repeat", selection: $repeat_) {
                        ForEach(ScheduleBuilder.Repeat.allCases, id: \.self) { option in
                            Text(option.label).tag(option)
                        }
                    }

                    if repeat_.usesTime {
                        DatePicker(
                            "At", selection: $timeOfDay, displayedComponents: .hourAndMinute
                        )
                    }

                    if repeat_ == .weekly {
                        WeekdayPicker(selected: $weekdays)
                    }

                    if repeat_ == .everyNHours {
                        Picker("Every", selection: $everyHours) {
                            ForEach([2, 3, 4, 6, 8, 12], id: \.self) { n in
                                Text("\(n) hours").tag(n)
                            }
                        }
                    }

                    Picker("Time zone", selection: $draft.tz) {
                        Text("Local time").tag("")
                        Divider()
                        // City first, region after.
                        //
                        // macOS menus jump to the first item matching what you
                        // type, but every identifier begins with a continent,
                        // so typing "Tokyo" matched nothing and the only way
                        // to a city was scrolling four hundred rows. "Tokyo,
                        // Asia" puts the word people actually know first.
                        ForEach(Self.timeZoneIdentifiers, id: \.identifier) { zone in
                            Text(zone.label).tag(zone.identifier)
                        }
                    }

                }

                Section {
                    // What goguma will do, stated, not a choice to make.
                    //
                    // Mark / Pattern / Wake only is a question about *how*
                    // goguma watches a process, which requires knowing that
                    // it watches processes at all, what a match pattern is,
                    // and which applies to your job. `import` already reasons
                    // this out and recommends; asking it cold on this sheet
                    // pushed an implementation detail onto whoever knows least.
                    //
                    // It is derived from the command instead, and can still be
                    // overridden below for anyone who knows they want to.
                    Text(detectionSummary)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)

                    if draft.detection == .pattern {
                        patternField
                    }

                    if draft.detection == .wakeOnly {
                        Text(Format.noWidow(wakeOnlyWindowNote))
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textTertiary)
                        .fixedSize(horizontal: false, vertical: true)
                    }
                } header: {
                    SectionLabel(text: "Detection")
                }

                Section {
                    // Optional, and only ever read back to the user: goguma
                    // never executes a job, so this is a label for the detail
                    // pane and a hint for pattern suggestion. It sat second in
                    // the first section, which implied it was required.
                    TextField(
                        "Command",
                        text: $draft.command,
                        prompt: Text("optional, shown in the job's details")
                    )
                    .font(Theme.Typography.code)
                    .help("Shown in the job's details. goguma never runs it.")

                    Picker("Watch for", selection: $draft.detection) {
                        ForEach(DetectionMode.selectable, id: \.self) { mode in
                            Text(mode.label).tag(mode)
                        }
                    }
                    .help("goguma picks this from the command. Change it only if "
                        + "you know the job behaves differently.")

                    TextField(
                        "Max runtime",
                        text: $maxRuntimeText,
                        prompt: Text("optional, goguma learns it")
                    )
                    .help("Overrides the learned ceiling, e.g. 90s, 5m, 1h30m.")

                    TextField(
                        "Wake buffer",
                        text: $wakeBufferText,
                        prompt: Text("optional, uses the global default")
                    )
                    .help("How early to wake the Mac before this job fires.")

                    // Only when editing. A job being added is being added
                    // because the user wants it, and offering to create it
                    // switched off is a question nobody arrives with.
                    if mode.isEdit {
                        Toggle("Enabled", isOn: $draft.enabled)
                    }
                } header: {
                    SectionLabel(text: "Advanced")
                } footer: {
                    if let durationProblem {
                        Text(durationProblem)
                            .font(Theme.Typography.caption)
                            .foregroundStyle(Theme.Colors.danger)
                    }
                }
            }
            // `.columns`, not `.grouped`.
            //
            // The grouped style draws its own opaque white cards, so this
            // sheet arrived as a stack of System Settings panels floating on
            // the app's surface, the single loudest reason it did not look
            // like the rest of goguma. Columns keeps the aligned label
            // gutter and inherits the surface underneath it.
            .formStyle(.columns)
            .font(Theme.Typography.rowLabel)
            .scrollContentBackground(.hidden)

            if let saveError {
                Text(saveError)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.danger)
                    .fixedSize(horizontal: false, vertical: true)
            }

            HStack {
                Spacer(minLength: 0)
                Button("Cancel", role: .cancel) {
                    testTask?.cancel()
                    dismiss()
                }
                .keyboardShortcut(.cancelAction)

                Button(mode.isEdit ? "Save" : "Add") { save() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .disabled(!isValid || isSaving)
            }
        }
        .padding(Theme.Space.lg)
        .onChange(of: draft.command) { _, _ in
            // Only while adding. In edit mode the job may carry a hand-tuned
            // pattern or mark-based detection the user set up deliberately;
            // re-deriving on a typo fix silently downgraded both.
            if !mode.isEdit { deriveDetection() }
        }
        .onChange(of: repeat_) { _, _ in rebuildSchedule() }
        .onChange(of: timeOfDay) { _, _ in rebuildSchedule() }
        .onChange(of: weekdays) { _, _ in rebuildSchedule() }
        .onChange(of: everyHours) { _, _ in rebuildSchedule() }
        .frame(width: Theme.Surface.editSheetWidth)
        .themeSurface()
        .onDisappear { testTask?.cancel() }
    }

    /// Names the actual window length so the cost of choosing wake-only is a
    /// number, not an abstraction. Falls back to prose when config hasn't
    /// loaded yet rather than guessing at the default.
    private var wakeOnlyWindowNote: String {
        let source = maxRuntimeText.isEmpty
            ? store.config?.wakeOnlyHold
            : WGDuration.parse(maxRuntimeText).map { WGDuration(seconds: $0) }
        guard let window = source, !window.isZero else {
            return "The window length comes from `wake_only_hold` in Settings."
        }
        return maxRuntimeText.isEmpty
            ? "The Mac will stay awake for \(window.displayString) each run, from the `wake_only_hold` "
                + "setting. Set a max runtime below to override it for this job."
            : "The Mac will stay awake for \(window.displayString) each run."
    }

    // MARK: - Pattern field

    private var patternField: some View {
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            HStack(spacing: Theme.Space.sm) {
                TextField("Match pattern", text: $draft.match, prompt: Text("hermes.*briefing"))
                    .font(Theme.Typography.code)
                    .onChange(of: draft.match) { _, _ in scheduleTest() }

                Button("Test", systemImage: Theme.Icon.test) { runTest(immediately: true) }
                    .buttonStyle(.bordered)
                    .disabled(draft.match.isEmpty || isTesting)
            }

            matchFeedback
        }
    }

    @ViewBuilder
    private var matchFeedback: some View {
        if isTesting {
            Text("Checking the process table…")
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
        } else if let matchError {
            Text(matchError)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.danger)
                .fixedSize(horizontal: false, vertical: true)
        } else if let result = matchResult {
            if !result.valid {
                Text(result.error.isEmpty ? "That isn't a valid regular expression." : result.error)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.danger)
                    .fixedSize(horizontal: false, vertical: true)
            } else if result.matches.isEmpty {
                Text(
                    "Valid, but nothing running matches it right now. That's expected if the job "
                        + "isn't running, but if it is, the pattern is wrong and this job will never be detected."
                )
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.warning)
                .fixedSize(horizontal: false, vertical: true)
            } else {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text("Matches \(Format.count(result.matches.count, "process")) right now:")
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.ok)
                    ScrollView {
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            ForEach(result.matches) { match in
                                HStack(alignment: .top, spacing: Theme.Space.xs) {
                                    Text("\(match.pid)")
                                        .font(Theme.Typography.tabularSmall)
                                        .foregroundStyle(Theme.Colors.textTertiary)
                                    Text(match.command)
                                        .font(Theme.Typography.code)
                                        .foregroundStyle(Theme.Colors.textSecondary)
                                        .lineLimit(1)
                                        .truncationMode(.middle)
                                }
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxHeight: Theme.Surface.editSheetMatchResultsHeight)
                }
            }
        }
    }

    /// Debounced live validation. Typing a regexp produces a lot of
    /// intermediate garbage; testing every keystroke would spam the daemon with
    /// process-table scans.
    private func scheduleTest() {
        testTask?.cancel()
        matchResult = nil
        matchError = nil
        guard !draft.match.isEmpty else { return }
        testTask = Task {
            try? await Task.sleep(for: .milliseconds(400))
            guard !Task.isCancelled else { return }
            runTest(immediately: false)
        }
    }

    private func runTest(immediately: Bool) {
        if immediately { testTask?.cancel() }
        let pattern = draft.match
        guard !pattern.isEmpty else { return }
        isTesting = true
        Task { @MainActor in
            defer { isTesting = false }
            do {
                matchResult = try await store.daemon.testMatch(pattern: pattern)
                matchError = nil
            } catch let error as DaemonError {
                matchResult = nil
                matchError = error.errorDescription
            } catch {
                matchResult = nil
                matchError = String(describing: error)
            }
        }
    }

    // MARK: - Validation and saving

    private var durationProblem: String? {
        if !maxRuntimeText.isEmpty, WGDuration.parse(maxRuntimeText) == nil {
            return "Max runtime isn't a duration. Try 90s, 5m, or 1h30m."
        }
        if !wakeBufferText.isEmpty, WGDuration.parse(wakeBufferText) == nil {
            return "Wake buffer isn't a duration. Try 90s, 5m, or 1h30m."
        }
        return nil
    }

    private var isValid: Bool {
        guard !draft.name.trimmingCharacters(in: .whitespaces).isEmpty else { return false }
        guard !draft.schedule.trimmingCharacters(in: .whitespaces).isEmpty else { return false }
        if draft.detection == .pattern,
           draft.match.trimmingCharacters(in: .whitespaces).isEmpty { return false }
        return durationProblem == nil
    }

    private func save() {
        var job = draft
        job.name = job.name.trimmingCharacters(in: .whitespaces)
        job.schedule = job.schedule.trimmingCharacters(in: .whitespaces)
        // The daemon derives an id from the name; mirror that derivation here so
        // a newly added job validates on the first hop rather than bouncing.
        if job.id.isEmpty { job.id = Job.slug(from: job.name) }
        if job.source.isEmpty { job.source = "manual" }
        job.maxRuntime = WGDuration(seconds: WGDuration.parse(maxRuntimeText) ?? 0)
        job.wakeBuffer = WGDuration(seconds: WGDuration.parse(wakeBufferText) ?? 0)
        if job.detection != .pattern { job.match = "" }

        isSaving = true
        saveError = nil
        let isEdit = mode.isEdit
        Task { @MainActor in
            defer { isSaving = false }
            do {
                // `jobs.put` upserts, which is what an edit wants: the job may
                // have been removed from under us while the sheet was open.
                if isEdit {
                    try await store.daemon.putJob(job)
                } else {
                    try await store.daemon.addJob(job)
                }
                onSaved()
                dismiss()
            } catch let error as DaemonError {
                saveError = error.errorDescription
            } catch {
                saveError = String(describing: error)
            }
        }
    }

    /// A time zone as the menu shows it: city first, sorted by city.
    struct ZoneChoice {
        let identifier: String
        let label: String
    }

    private static let timeZoneIdentifiers: [ZoneChoice] = TimeZone.knownTimeZoneIdentifiers
        .map { identifier in
            let parts = identifier.split(separator: "/")
            guard parts.count > 1 else { return ZoneChoice(identifier: identifier, label: identifier) }
            let city = parts.last!.replacingOccurrences(of: "_", with: " ")
            let region = parts.dropLast().joined(separator: "/")
                .replacingOccurrences(of: "_", with: " ")
            return ZoneChoice(identifier: identifier, label: "\(city), \(region)")
        }
        .sorted { $0.label.localizedCaseInsensitiveCompare($1.label) == .orderedAscending }


    private func rebuildSchedule() {
        draft.schedule = ScheduleBuilder.expression(
            repeating: repeat_, at: timeOfDay, weekdays: weekdays, everyHours: everyHours
        )
    }

    /// What goguma will actually do, in a sentence.
    private var detectionSummary: String {
        switch draft.detection {
        case .mark:
            "goguma will hold sleep off until the job reports that it has finished. "
                + "Wrap it in `goguma-mark` in your crontab for this to work."
        case .pattern:
            draft.match.isEmpty
                ? "goguma will watch for the job's process and let the Mac sleep when it exits."
                : "goguma will watch for `\(draft.match)` and let the Mac sleep when it exits."
        case .wakeOnly, .unknown:
            // `.unknown` is a mode a newer daemon knows and this app does not.
            // Describing it as the fixed window is the safe reading: that is
            // what any unrecognised mode degrades to here.
            "goguma can't tell when this job finishes, so it will keep the Mac awake for a "
                + "set window and then let it sleep."
        }
    }

    /// Chooses how to watch a job from the command the user gave.
    ///
    /// A command distinctive enough to match is watched; anything else falls
    /// back to a fixed window, which always works and never lies. Mark is never
    /// chosen automatically; it requires the user to edit their crontab, so
    /// selecting it on their behalf would promise something not yet true.
    private func deriveDetection() {
        let pattern = Self.suggestPattern(draft.command)
        if pattern.isEmpty {
            draft.detection = .wakeOnly
            draft.match = ""
        } else {
            draft.detection = .pattern
            draft.match = pattern
        }
    }

    /// Swift counterpart of `internal/detect.SuggestPattern`.
    ///
    /// Same rule: anchor on the program, then the last positional argument,
    /// which is where task runners put the job's identity. Flags, their
    /// values, paths and quoted prose are all skipped, a pattern built from a
    /// prompt is neither stable nor unique.
    static func suggestPattern(_ command: String) -> String {
        // Order mirrors Go: unwrap `sh -c "..."` first (its payload lives
        // inside the quotes), THEN drop quoted prose, THEN take the last
        // shell segment. The old version skipped the unwrap (deriving the
        // pattern `sh`, which matches every shell on the machine), split
        // only on "&&", and never regex-escaped, so it could emit patterns
        // the daemon rejects or that match unrelated processes.
        let cmd = lastShellSegment(stripQuoted(stripShellWrapping(command)))
        let fields = cmd.split(separator: " ").map(String.init).filter { !$0.isEmpty }
        guard let first = fields.first else { return "" }
        let program = first.components(separatedBy: "/").last ?? first
        guard !program.isEmpty else { return "" }

        var positional: [String] = []
        var previousWasFlag = false
        for field in fields.dropFirst() {
            let isFlag = field.hasPrefix("-")
            if !isFlag, !previousWasFlag,
               field.rangeOfCharacter(from: CharacterSet(charactersIn: "/><|&$`\"'")) == nil,
               field.rangeOfCharacter(from: CharacterSet.decimalDigits.inverted) != nil {
                positional.append(field)
            }
            previousWasFlag = isFlag && !field.contains("=")
        }

        // The last identifier distinguishes; one earlier one adds context,
        // same as Go's SuggestPattern. Everything is escaped: a command
        // containing regex metacharacters must not produce a pattern the
        // daemon refuses to save.
        var parts = [NSRegularExpression.escapedPattern(for: program)]
        if positional.count >= 2 {
            parts.append(NSRegularExpression.escapedPattern(for: positional[positional.count - 2]))
        }
        if let identity = positional.last {
            parts.append(NSRegularExpression.escapedPattern(for: identity))
        }
        return parts.joined(separator: ".*")
    }

    /// Unwraps the `/bin/sh -c "..."` form crontab entries commonly use.
    private static func stripShellWrapping(_ command: String) -> String {
        let fields = command.split(separator: " ").map(String.init)
        for i in 0..<max(fields.count - 1, 0) {
            let base = fields[i].components(separatedBy: "/").last ?? fields[i]
            if ["sh", "bash", "zsh"].contains(base), fields[i + 1] == "-c" {
                let rest = fields[(i + 2)...].joined(separator: " ")
                return rest.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
            }
        }
        return command
    }

    /// Removes single- and double-quoted runs; prose makes unstable patterns.
    private static func stripQuoted(_ command: String) -> String {
        var out = ""
        var quote: Character? = nil
        for ch in command {
            if let q = quote {
                if ch == q { quote = nil }
            } else if ch == "'" || ch == "\"" {
                quote = ch
                out.append(" ")
            } else {
                out.append(ch)
            }
        }
        return out
    }

    /// The final command of a `cd x && real-command` or `a; b` chain.
    private static func lastShellSegment(_ command: String) -> String {
        var out = command
        for sep in ["&&", ";", "||"] {
            if let r = out.range(of: sep, options: .backwards) {
                out = String(out[r.upperBound...])
            }
        }
        return out.trimmingCharacters(in: .whitespaces)
    }
}


/// Common schedules, so writing cron is a choice rather than the entry fee.
struct SchedulePreset: Identifiable {
    let label: String
    let expression: String
    var id: String { expression }

    /// Sentinel for "the expression is not one of ours": the user has typed
    /// their own, and the menu must show that rather than silently claiming
    /// whichever preset happens to sort first.
    static let customTag = "__custom__"

    static let all: [SchedulePreset] = [
        .init(label: "Every hour", expression: "0 * * * *"),
        .init(label: "Every 6 hours", expression: "0 */6 * * *"),
        .init(label: "Every day at 09:00", expression: "0 9 * * *"),
        .init(label: "Every day at 18:00", expression: "0 18 * * *"),
        .init(label: "Twice a day, 09:00 and 21:00", expression: "0 9,21 * * *"),
        .init(label: "Weekdays at 09:00", expression: "0 9 * * 1-5"),
        .init(label: "Every Monday at 09:00", expression: "0 9 * * 1"),
        .init(label: "First of the month at 09:00", expression: "0 9 1 * *"),
    ]
}
