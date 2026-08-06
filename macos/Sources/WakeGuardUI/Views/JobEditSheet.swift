import SwiftUI

/// Add / edit a job.
///
/// The pattern tester is the point of this sheet. Pattern detection fails
/// silently at 3am when the regexp doesn't match, so the sheet asks the daemon
/// to evaluate the pattern against the live process table while the user types
/// — a wrong pattern is visible here, not a month later in the history.
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
    @State private var isSaving = false
    @State private var testTask: Task<Void, Never>?

    init(store: StatusStore, mode: Mode, onSaved: @escaping () -> Void) {
        self.store = store
        self.mode = mode
        self.onSaved = onSaved
        switch mode {
        case .add:
            _draft = State(initialValue: Job())
            _maxRuntimeText = State(initialValue: "")
            _wakeBufferText = State(initialValue: "")
        case let .edit(job):
            _draft = State(initialValue: job)
            _maxRuntimeText = State(initialValue: job.maxRuntime.isZero ? "" : job.maxRuntime.wireString)
            _wakeBufferText = State(initialValue: job.wakeBuffer.isZero ? "" : job.wakeBuffer.wireString)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.md) {
            Text(mode.isEdit ? "Edit Job" : "Add Job")
                .font(Theme.Typography.title)
                .foregroundStyle(Theme.Colors.textPrimary)

            Form {
                Section {
                    TextField("Name", text: $draft.name)
                        .disabled(mode.isEdit)
                        .help(
                            mode.isEdit
                                ? "A job's name is its identity on disk and in IPC, so it can't be changed here."
                                : "Used as the job's id. Lower-cased and hyphenated."
                        )

                    TextField("Schedule", text: $draft.schedule, prompt: Text("0 9 * * *"))
                        .font(Theme.Typography.code)

                    Picker("Time zone", selection: $draft.tz) {
                        Text("Local time").tag("")
                        Divider()
                        ForEach(Self.timeZoneIdentifiers, id: \.self) { identifier in
                            Text(identifier).tag(identifier)
                        }
                    }

                    TextField(
                        "Command",
                        text: $draft.command,
                        prompt: Text("what your crontab actually runs (optional)")
                    )
                    .font(Theme.Typography.code)
                } header: {
                    SectionLabel(text: "Schedule")
                }

                Section {
                    Picker("Detection", selection: $draft.detection) {
                        ForEach(DetectionMode.selectable, id: \.self) { mode in
                            Text(mode.label).tag(mode)
                        }
                    }
                    .pickerStyle(.segmented)

                    if draft.detection == .pattern {
                        patternField
                    }
                    // Every mode explains its own trade-off, including
                    // wake-only, which is a legitimate choice rather than a
                    // fallback and should not read like one.
                    Text(draft.detection.explanation)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)

                    if draft.detection == .wakeOnly {
                        Text(
                            wakeOnlyWindowNote
                        )
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textTertiary)
                        .fixedSize(horizontal: false, vertical: true)
                    }
                } header: {
                    SectionLabel(text: "Detection")
                }

                Section {
                    TextField(
                        "Max runtime",
                        text: $maxRuntimeText,
                        prompt: Text("leave empty to let WakeGuard learn it")
                    )
                    .help("Overrides the learned ceiling, e.g. 90s, 5m, 1h30m.")

                    TextField(
                        "Wake buffer",
                        text: $wakeBufferText,
                        prompt: Text("leave empty to use the global default")
                    )
                    .help("How early to wake the Mac before this job fires.")

                    Toggle("Enabled", isOn: $draft.enabled)
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
            .formStyle(.grouped)

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

    private static let timeZoneIdentifiers: [String] = TimeZone.knownTimeZoneIdentifiers.sorted()
}
