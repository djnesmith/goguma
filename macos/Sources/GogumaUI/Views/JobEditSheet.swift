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
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var draft: Job
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
    @State private var everyMinutes = 15
    @State private var dayOfMonth = 1
    @State private var showAdvanced = false

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
        case let .edit(job):
            _draft = State(initialValue: job)
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
                _everyMinutes = State(initialValue: seed.everyMinutes)
                _dayOfMonth = State(initialValue: seed.dayOfMonth)
            }
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.md) {
            Text(mode.isEdit ? "Edit Job" : "Add Job")
                .font(Theme.Typography.title)
                .foregroundStyle(Theme.Colors.textPrimary)

            // One Grid for the whole sheet, not a Form.
            //
            // A Form sizes its label gutter from the rows it can currently
            // see, so opening Advanced measured four new labels, widened the
            // gutter, and slid Name, Repeat, At and Time zone sideways. The
            // disclosure was redrawing the page above it. One grid measures
            // the column once, from every label including the hidden ones, so
            // opening Advanced does exactly one thing: reveal rows.
            //
            // It is also how the Settings pane is built, so the two surfaces
            // now share an alignment instead of each having their own.
            Grid(
                alignment: .leadingFirstTextBaseline,
                horizontalSpacing: Theme.Space.sm,
                verticalSpacing: Theme.Space.sm
            ) {
                scheduleSection
                detectionSection
                advancedSection
            }
            .font(Theme.Typography.rowLabel)

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
        .onChange(of: everyMinutes) { _, _ in rebuildSchedule() }
        .onChange(of: dayOfMonth) { _, _ in rebuildSchedule() }
        .frame(width: Theme.Surface.editSheetWidth)
        .themeSurface()
        .onDisappear { testTask?.cancel() }
    }

    // MARK: - Rows

    /// A labelled row on the sheet's single grid.
    ///
    /// Labels are trailing-aligned, the Mac convention for a sheet where every
    /// row has one, and the column is as wide as the widest label in the whole
    /// sheet, not the widest currently visible.
    @ViewBuilder
    private func row(_ label: String, @ViewBuilder _ control: () -> some View) -> some View {
        GridRow {
            Text(label)
                .foregroundStyle(Theme.Colors.textPrimary)
                .lineLimit(1)
                .frame(width: Self.labelWidth, alignment: .trailing)
            control()
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// The label column, fixed.
    ///
    /// Wide enough for the longest label the sheet can ever show, and the same
    /// whatever is on screen. A measured column moved every row sideways when
    /// Advanced opened, and a zero-height reservation row does not work: a
    /// view framed to zero height reports zero width, so the grid measured
    /// nothing at all.
    ///
    /// The number matters. Every label is right-aligned against this, so one
    /// long label sets how far from the left margin the entire sheet begins:
    /// at 152, sized for "How goguma watches it", "Name" sat a third of the
    /// way across the sheet and the whole form read as floating in the middle.
    /// Labels are kept short enough to hold this at 108, which fits the
    /// longest of them ("How it's watched") without truncating and still
    /// starts the form 44pt further left than the old measured column did.
    private static let labelWidth: CGFloat = 108

    // MARK: - When it runs

    @ViewBuilder
    private var scheduleSection: some View {
        row("Name") {
            TextField("", text: $draft.name)
                .labelsHidden()
                // A job name is two or three words. Filling the sheet's whole
                // width implied a sentence was wanted.
                .frame(width: 220)
                .focused($focusedField, equals: .name)
                .disabled(mode.isEdit)
                .help(
                    mode.isEdit
                        ? "A job's name is its identity on disk and in IPC, so it can't be changed here."
                        : "Used as the job's id. Lower-cased and hyphenated."
                )
        }

        // An alarm, not an expression.
        //
        // "Custom…" used to reveal a cron field, which is the thing the
        // presets existed to avoid: anyone who fell off the preset list landed
        // on `0 9 * * *` with no way to reason about it. These are the controls
        // a person already knows from setting an alarm.
        row("Repeat") {
            Picker("", selection: $repeat_) {
                ForEach(ScheduleBuilder.Repeat.allCases, id: \.self) { option in
                    Text(option.label).tag(option)
                }
            }
            .labelsHidden()
            .fixedSize()
        }

        scheduleDetail

        row("Time zone") {
            Picker("", selection: $draft.tz) {
                Text("Local time").tag("")
                Divider()
                ForEach(Self.timeZoneChoices, id: \.identifier) { zone in
                    Text(zone.label).tag(zone.identifier)
                }
            }
            .labelsHidden()
            .fixedSize()
        }
    }

    /// The controls that only apply to one kind of repeat.
    @ViewBuilder
    private var scheduleDetail: some View {
        if repeat_ == .weekly {
            row("On") { WeekdayPicker(selected: $weekdays) }
        }

        if repeat_ == .monthly {
            row("Day") {
                Picker("", selection: $dayOfMonth) {
                    ForEach(1...28, id: \.self) { day in
                        Text(Self.ordinal(day)).tag(day)
                    }
                }
                .labelsHidden()
                .fixedSize()
                .help("Capped at the 28th, the last day every month has.")
            }
        }

        if repeat_ == .everyNHours {
            row("Every") {
                Picker("", selection: $everyHours) {
                    ForEach(ScheduleBuilder.hourChoices, id: \.self) { n in
                        Text(n == 1 ? "hour" : "\(n) hours").tag(n)
                    }
                }
                .labelsHidden()
                .fixedSize()
            }
        }

        if repeat_ == .everyNMinutes {
            row("Every") {
                Picker("", selection: $everyMinutes) {
                    ForEach(ScheduleBuilder.minuteChoices, id: \.self) { n in
                        Text(n == 1 ? "minute" : "\(n) minutes").tag(n)
                    }
                }
                .labelsHidden()
                .fixedSize()
            }
        }

        if repeat_.usesTime {
            row("At") { ClockPicker(date: $timeOfDay) }
        }
    }

    // MARK: - What goguma will do

    /// One line, in the label column like everything else.
    @ViewBuilder
    private var detectionSection: some View {
        row("While it runs") {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text(detectionLine)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)

                if draft.detection == .pattern {
                    patternField
                }
            }
        }
    }

    /// What goguma will do, in one sentence.
    private var detectionLine: String {
        switch draft.detection {
        case .mark:
            return "Waits for the job to report that it has finished."
        case .pattern:
            return draft.match.isEmpty
                ? "Watches for the job's process, and lets the Mac sleep when it exits."
                : "Watches for `\(draft.match)`, and lets the Mac sleep when it exits."
        case .wakeOnly, .unknown:
            // `.unknown` is a mode a newer daemon knows and this app does not.
            // Describing it as the fixed window is the safe reading: that is
            // what any unrecognised mode degrades to here.
            guard let window = wakeOnlyWindow else {
                return "Can't tell when this job finishes, so it stays awake for a set time."
            }
            return "Can't tell when this job finishes, so it stays awake "
                + "\(window.displayString) each run."
        }
    }

    /// The window this job will actually hold, or nil before config loads.
    private var wakeOnlyWindow: WGDuration? {
        let source = draft.maxRuntime.isZero ? store.config?.wakeOnlyHold : draft.maxRuntime
        guard let source, !source.isZero else { return nil }
        return source
    }

    // MARK: - Advanced

    /// The word is the control, and opening it only adds rows.
    @ViewBuilder
    private var advancedSection: some View {
        GridRow {
            Button {
                withAnimation(Theme.motion(reduced: reduceMotion)) { showAdvanced.toggle() }
            } label: {
                HStack(spacing: Theme.Space.xxs) {
                    Spacer(minLength: 0)
                    Image(systemName: Theme.Icon.disclosure)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textTertiary)
                        .rotationEffect(.degrees(showAdvanced ? 90 : 0))
                    Text("Advanced")
                        .foregroundStyle(Theme.Colors.textPrimary)
                }
                // The word and its chevron, both clickable. A disclosure whose
                // label does nothing is a disclosure people report as broken.
                .contentShape(.rect)
                .frame(width: Self.labelWidth, alignment: .trailing)
            }
            .buttonStyle(.plain)

            Color.clear.frame(height: 1)
        }

        if showAdvanced {
            advancedFields
        }
    }

    @ViewBuilder
    private var advancedFields: some View {
        // Optional, and only ever read back to the user: goguma never executes
        // a job, so this is a label for the detail pane and a hint for pattern
        // suggestion.
        row("Command") {
            TextField("", text: $draft.command, prompt: Text("optional, shown in the job's details"))
                .labelsHidden()
                // The sheet's own type, not monospace.
                //
                // A code font here made this one row look like a terminal
                // dropped into a settings sheet, at a different size and
                // colour from the field directly above it.
                .frame(width: 260)
                .help("Shown in the job's details. goguma never runs it.")
        }

        row("How it's watched") {
            Picker("", selection: $draft.detection) {
                ForEach(DetectionMode.selectable, id: \.self) { mode in
                    Text(mode.label).tag(mode)
                }
            }
            .labelsHidden()
            .fixedSize()
            .help("goguma picks this from the command. Change it only if you know "
                + "the job behaves differently.")
        }

        // Menus, not text fields. These took typed durations ("90s", "1h30m"),
        // a format the sheet never taught, and a mistyped one is a job held
        // for the wrong length.
        row("Stay awake for") {
            DurationPicker(
                presets: [60, 120, 300, 600, 1800, 3600].map { WGDuration(seconds: $0) },
                current: draft.maxRuntime,
                includeZeroAs: "Whatever goguma learns"
            ) { draft.maxRuntime = $0 }
                .help("How long to hold sleep off for this job, instead of the length "
                    + "goguma works out from its history.")
        }

        row("Wake early") {
            DurationPicker(
                presets: [30, 60, 90, 120, 300].map { WGDuration(seconds: $0) },
                current: draft.wakeBuffer,
                includeZeroAs: "Same as every other job"
            ) { draft.wakeBuffer = $0 }
                .help("How far before its time to wake the Mac, so it is ready when the job fires.")
        }

        // Only when editing. A job being added is being added because the user
        // wants it, and offering to create it switched off is a question
        // nobody arrives with.
        if mode.isEdit {
            row("Enabled") {
                Toggle("", isOn: $draft.enabled).labelsHidden()
            }
        }
    }

    /// "1st", "2nd", "3rd" …
    static func ordinal(_ n: Int) -> String {
        let suffix: String
        switch (n % 10, n % 100) {
        case (1, 11), (2, 12), (3, 13): suffix = "th"
        case (1, _): suffix = "st"
        case (2, _): suffix = "nd"
        case (3, _): suffix = "rd"
        default: suffix = "th"
        }
        return "\(n)\(suffix)"
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

    /// Nothing to check any more.
    ///
    /// This parsed two typed duration fields and reported "not a duration".
    /// Both are menus now, so an invalid value cannot be produced, and a
    /// validator for a state that cannot occur is a claim the sheet has to
    /// keep true forever for no benefit.
    private var durationProblem: String? { nil }

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

    /// A time zone as the menu shows it.
    struct ZoneChoice {
        let identifier: String
        let label: String
    }

    /// The time zones, rather than every place on earth.
    ///
    /// This was `TimeZone.knownTimeZoneIdentifiers`, which is around six
    /// hundred rows: every city Apple ships, most of which share an offset and
    /// exist only to record a historical difference. Scrolling six hundred
    /// rows to find your own is not a choice, it is a search, and rebuilding
    /// that menu on every state change is most of why changing Repeat felt
    /// slow.
    ///
    /// Real identifiers rather than fixed offsets, because an offset is not a
    /// time zone: "GMT-5" is New York in winter and Bogotá all year, and
    /// storing the wrong one moves a job by an hour for half of it. One
    /// canonical zone per region people actually schedule against, and the
    /// offset shown beside it so it can be picked by offset when the city is
    /// unfamiliar.
    private static let zoneIdentifiers = [
        "Pacific/Midway", "Pacific/Honolulu", "America/Anchorage",
        "America/Los_Angeles", "America/Denver", "America/Chicago",
        "America/New_York", "America/Halifax", "America/Sao_Paulo",
        "Atlantic/Azores", "UTC", "Europe/London", "Europe/Paris",
        "Europe/Athens", "Europe/Moscow", "Asia/Dubai", "Asia/Karachi",
        "Asia/Kolkata", "Asia/Dhaka", "Asia/Bangkok", "Asia/Singapore",
        "Asia/Shanghai", "Asia/Tokyo", "Australia/Adelaide", "Australia/Sydney",
        "Pacific/Auckland",
    ]

    static let timeZoneChoices: [ZoneChoice] = zoneIdentifiers
        .compactMap { identifier -> (ZoneChoice, Int)? in
            guard let zone = TimeZone(identifier: identifier) else { return nil }
            let offset = zone.secondsFromGMT()
            let city = identifier.split(separator: "/").last
                .map { $0.replacingOccurrences(of: "_", with: " ") } ?? identifier
            return (ZoneChoice(identifier: identifier, label: "\(city)  \(gmtLabel(offset))"), offset)
        }
        // By offset, so the menu reads as a line around the world rather than
        // as an alphabetical list of unrelated cities.
        .sorted { $0.1 < $1.1 }
        .map(\.0)

    private static func gmtLabel(_ seconds: Int) -> String {
        if seconds == 0 { return "GMT" }
        let sign = seconds < 0 ? "-" : "+"
        let total = abs(seconds) / 60
        let hours = total / 60, minutes = total % 60
        return minutes == 0
            ? "GMT\(sign)\(hours)"
            : String(format: "GMT%@%d:%02d", sign, hours, minutes)
    }

    /// "at 15 minutes past", for a repeat with no hour of its own.
    static func pastTheHour(_ date: Date) -> String {
        let minute = Calendar.current.component(.minute, from: date)
        return minute == 0 ? "on the hour" : "at \(minute) minutes past"
    }

    private func rebuildSchedule() {
        draft.schedule = ScheduleBuilder.expression(
            repeating: repeat_, at: timeOfDay, weekdays: weekdays,
            everyHours: everyHours, everyMinutes: everyMinutes, dayOfMonth: dayOfMonth
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
