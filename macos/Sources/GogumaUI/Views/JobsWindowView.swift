import SwiftUI

/// The Jobs window: every registered schedule, what it costs, and whether it is
/// actually working.
///
/// **Why this is a list and not a table.** It was an eight-column `Table`
/// (enabled, name, schedule, next run, typical, ceiling, detection, recent)
/// above a detail pane of a dozen label/value rows, under a row of six bordered
/// buttons. Every column was defensible and the result was a spreadsheet: a
/// grid of small grey text with no shape to it, where the eye had nowhere to
/// land and nothing indicated which of seven rows needed attention.
///
/// A table earns its density when there are hundreds of rows to scan and
/// compare column-wise. This list holds however many schedules one person has,
/// and the questions asked of it are per-row: is this on, when does it next
/// run, is it healthy. So each job is one two-line row with room to breathe,
/// the numbers move to the detail pane where they can be read properly, and the
/// selection-dependent actions live with the selection instead of greying out a
/// permanent button bar.
struct JobsWindowView: View {
    @Bindable var store: StatusStore
    let coordinator: WindowCoordinator

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// Seeds `selection`. Only the offscreen renderer passes it: a detail
    /// pane that needs a click to appear cannot otherwise be screenshotted.
    var initialSelection: JobView.ID?

    @State private var selection: JobView.ID?
    @FocusState private var queryFocused: Bool

    /// Which row the pointer is over.
    ///
    /// Rows had no hover state, so nothing on this window said they could be
    /// clicked; the detail pane asked the user to select a job while the list
    /// looked like a read-only table. An affordance nobody can see is the same
    /// as a feature that does not exist.
    @State private var hoveredID: JobView.ID?
    @State private var editing: JobEditSheet.Mode?
    @State private var pendingRemoval: JobView?
    @State private var renaming: JobView?
    @State private var newGroupName = ""
    @State private var query = ""
    /// Whether anything has been selected since this window opened.
    ///
    /// Once it has, the detail pane keeps its height even after a deselect.
    /// Letting it collapse back meant every deselect resized the pane and threw
    /// the list down the window, the layout moving underneath the very click
    /// that caused it. It resets when the window is next opened, so a session
    /// that never selects anything never pays for the space.
    /// Collapsed group names, remembered across launches. Stored as one string
    /// because `@AppStorage` cannot hold a Set; the list is short and this
    /// avoids a second persistence mechanism for a handful of names.
    @AppStorage("jobs.collapsedGroups") private var collapsedRaw = ""

    var body: some View {
        VStack(spacing: 0) {
            header
            ThemeHairline()

            if store.connection.blocksContent {
                DaemonUnavailableView(error: store.connection.error) {
                    Task { await store.refresh() }
                }
            } else if store.jobs.isEmpty {
                EmptyStateView(
                    icon: Theme.Icon.jobs,
                    title: "No jobs registered",
                    detail: "Add a job, or import the ones you already have with `goguma import`."
                )
            } else {
                searchField
                ThemeHairline()
                columnHeader
                jobList
                ThemeHairline()
                overnightCost
                ThemeHairline()
                detailPane
            }
        }
        .frame(
            minWidth: Theme.Surface.jobsWindowMinSize.width,
            minHeight: Theme.Surface.jobsWindowMinSize.height
        )
        .themeSurface()
        .pollsDaemon(store, as: .jobsWindow)
        .task {
            if selection == nil, let initialSelection { selection = initialSelection }
        }
        .sheet(item: $editing) { mode in
            JobEditSheet(store: store, mode: mode) {
                Task { await store.refresh() }
            }
        }
        .alert(
            "New group",
            isPresented: Binding(
                get: { renaming != nil },
                set: { if !$0 { renaming = nil; newGroupName = "" } }
            )
        ) {
            TextField("Group name", text: $newGroupName)
            Button("Move") {
                if let view = renaming { move(view, to: newGroupName) }
                renaming = nil
                newGroupName = ""
            }
            .disabled(newGroupName.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel", role: .cancel) { renaming = nil; newGroupName = "" }
        } message: {
            Text(renaming.map { "Move “\($0.job.displayName)” into a new group." } ?? "")
        }
        .confirmationDialog(
            pendingRemoval.map { "Remove “\($0.job.displayName)”?" } ?? "Remove job?",
            isPresented: Binding(
                get: { pendingRemoval != nil },
                set: { if !$0 { pendingRemoval = nil } }
            ),
            titleVisibility: .visible
        ) {
            // A managed job is re-adopted on the next sync, so removing it is
            // very nearly a no-op. Offer the action that actually does what the
            // user meant first, and keep removal available but honest.
            if pendingRemoval?.job.managed == true {
                Button("Disable Instead") {
                    if let job = pendingRemoval {
                        store.perform("Disabled \(job.job.displayName).") { client in
                            try await client.setJobEnabled(ref: job.job.id, enabled: false)
                        }
                    }
                    pendingRemoval = nil
                }
                Button("Remove Anyway", role: .destructive) { removePending() }
                Button("Cancel", role: .cancel) { pendingRemoval = nil }
            } else {
                Button("Remove", role: .destructive) { removePending() }
                Button("Cancel", role: .cancel) { pendingRemoval = nil }
            }
        } message: {
            if pendingRemoval?.job.managed == true {
                Text(
                    "goguma adopted this job automatically, so the next sync will add it straight "
                        + "back. Disabling it keeps it out of the way for good, it stays listed but "
                        + "never wakes the Mac. It retires on its own once it disappears from its source."
                )
            } else {
                Text(
                    "goguma will stop waking the Mac for this job. Its run history is kept, and the "
                        + "underlying cron or launchd entry isn't touched."
                )
            }
        }
    }

    private func removePending() {
        if let job = pendingRemoval {
            store.perform("Removed \(job.job.displayName).") { client in
                try await client.removeJob(ref: job.job.id)
            }
        }
        pendingRemoval = nil
    }

    private var selectedJob: JobView? {
        guard let selection else { return nil }
        return store.job(withID: selection)
    }

    // MARK: - Header

    /// A title and one primary action, rather than six buttons of equal weight.
    ///
    /// Only "Add Job" is always available and always meaningful; Edit, Remove
    /// and History all need a selection, and a row of permanently-greyed buttons
    /// is both ugly and a poor signal. Those three now live on the selected job.
    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("Jobs")
                    .font(Theme.Typography.title)
                    .foregroundStyle(Theme.Colors.heading)
                    .themeHeading()
                Text(summary)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .lineLimit(1)
            }

            Spacer(minLength: Theme.Space.md)

            if let message = store.actionMessage {
                ActionMessageView(message: message)
                    .frame(maxWidth: 260)
                    .transition(.opacity)
            }

            // One button, and it keeps its label.
            //
            // There were two here, both icon-only and both a circular arrow,
            // indistinguishable at a glance and impossible to tell apart by
            // hovering one at a time. Worse, one of them did nothing: this
            // window polls the daemon once a second while it is open, so
            // "Refresh" was a control for something already happening sixty
            // times a minute. Sync is different; it re-reads the watched
            // schedulers and adopts jobs the poll will not pick up, so it is
            // the one that survives, with the word on it.
            Button("Check for New Jobs", systemImage: Theme.Icon.sync) {
                store.performReporting { try await $0.sync().summary }
            }
            .disabled(store.connection.blocksContent || store.isPerformingAction)
            .help(
                "Re-read watched schedulers now and adopt anything new. This happens on its own "
                    + "every couple of minutes; use this when you have just created a job."
            )

            // Deliberately not `.borderedProminent`. A large filled pill is the
            // framework's default gesture toward "this is the primary action",
            // and it spends the loudest thing on screen on window chrome. The
            // strong fills in this window belong to status and data.
            Button("Add Job", systemImage: Theme.Icon.add) { editing = .add }
                .disabled(store.connection.blocksContent)
        }
        .buttonStyle(.bordered)
        .controlSize(.regular)
        .padding(.horizontal, Theme.Space.md)
        .padding(.vertical, Theme.Space.sm)
    }

    /// True when the list contains both adopted and hand-added jobs. See
    /// `JobRow.showsManagedBadge`.
    private var hasMixedSources: Bool {
        let managed = store.jobs.filter(\.job.managed).count
        return managed > 0 && managed < store.jobs.count
    }

    /// Count, then the single most useful fact: when the next thing happens.
    ///
    /// "Enabled" is gone. It read `9` beside `9 jobs` on any machine where
    /// nothing is switched off, which is most of them, a number that is almost
    /// always the same as the one next to it is not information. Only the
    /// disabled count is worth saying, and only when it is not zero.
    private var summary: String {
        let total = store.jobs.count
        let off = store.jobs.filter { !$0.job.enabled }.count
        var parts = [Format.count(total, "job")]
        if off > 0 { parts.append("\(off) disabled") }
        if let next = soonestJob, let fire = next.nextFire.meaningful {
            parts.append("next in \(Format.gap(fire, from: store.now))")
        }
        let trouble = store.jobs.filter { $0.worstSeverity != nil }.count
        if trouble > 0 { parts.append(Format.count(trouble, "needs attention", plural: "need attention")) }
        return parts.joined(separator: " · ")
    }

    // MARK: - Column header

    /// Names the columns, because three of them are bare numbers.
    ///
    /// The list had none: a row read "4x daily / 8h 39m / 1.2%" with nothing
    /// saying that the middle figure is when it next runs rather than how long
    /// it takes, or that the last one is a night's battery rather than a
    /// percentage of anything else. The name column is deliberately unlabelled;
    /// a column of job names does not need to be told it is one.
    ///
    /// Widths are the row's own, so the two line up. Any change to a column
    /// here has to be made in `JobRow` as well; they are matched by hand
    /// because the row's leading controls are not a column and cannot share a
    /// layout with one.
    private var columnHeader: some View {
        HStack(spacing: Theme.Space.sm) {
            Spacer(minLength: 0)

            Text("Schedule")
                .frame(width: 116, alignment: .leading)
            Text("Next run")
                .frame(width: 84, alignment: .trailing)
            Text("Per run")
                .frame(width: 72, alignment: .trailing)
                .help("Battery one firing of this job costs, averaged over the runs "
                    + "that happened on battery.")
            Text("Per sleep*")
                .frame(width: 80, alignment: .trailing)
                .help("Battery this job is projected to cost across a night asleep.")
            Color.clear.frame(width: Theme.IconSize.hero, height: 1)
        }
        .font(Theme.Typography.sectionLabel)
        .foregroundStyle(Theme.Colors.textTertiary)
        .lineLimit(1)
        .padding(.horizontal, Theme.Space.md)
        // Tight under the headings.
        //
        // Equal padding above and below left a band of empty surface between
        // the headings and the first row, so the headings floated rather than
        // sitting on the column they name.
        .padding(.top, Theme.Space.xs)
        .padding(.bottom, Theme.Space.xxs)
    }

    // MARK: - Overnight cost

    /// What the enabled set costs a sleeping Mac over eight hours, and which
    /// jobs make up that number.
    ///
    /// A total on its own is a fact nobody can act on. The point of this row is
    /// the breakdown beside it: "4.8%" tells you the morning will be worse, and
    /// "3.9% of it is the uptime check" tells you what to do about it. Ordered
    /// by cost so the answer is the first thing read.
    ///
    /// Only jobs measured on battery appear. A job that has never run on
    /// battery has no evidence behind it, and folding a guess into a total
    /// makes the whole number a guess.
    @ViewBuilder
    private var overnightCost: some View {
        let measured = store.jobs
            .filter { $0.job.enabled && $0.hasMeasuredCost && $0.nightlyBatteryPct > 0 }
            .sorted { $0.nightlyBatteryPct > $1.nightlyBatteryPct }
        let total = measured.reduce(0) { $0 + $1.nightlyBatteryPct }

        // The footnote is tied to the column, not to the total.
        //
        // It used to live inside this block, so on a machine with nothing
        // measured yet the header still said "Per sleep*" and the asterisk
        // pointed at nothing at all.
        // Two lines, not three.
        //
        // "Overnight" and its value were stacked, which made the label a line
        // of its own carrying one word in a third font, and put the breakdown
        // beside a two-line block so the row had a hole in it. The label, the
        // number and the breakdown are one sentence about one thing, so they
        // sit on one line, and the footnote takes the second.
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            if !measured.isEmpty {
                // One size, one shape, differing only in weight and colour.
                //
                // This line carried three type treatments in a row: an 11pt
                // tertiary label, a 13pt monospaced-digit value, and an 11pt
                // secondary sentence. Three fonts across four words reads as a
                // layout accident rather than as emphasis, and the mixed
                // baselines made the row look crooked. Everything is caption
                // size now; the total is the only thing set heavier, because
                // it is the only thing worth reading first.
                HStack(spacing: Theme.Space.xs) {
                    Text("Overnight")
                        .foregroundStyle(Theme.Colors.textTertiary)

                    Text(String(format: "%.1f%%", total))
                        .fontWeight(.medium)
                        .foregroundStyle(
                            total >= 10 ? Theme.Colors.warning : Theme.Colors.textPrimary
                        )

                    if !measured.isEmpty {
                        Text("·")
                            .foregroundStyle(Theme.Colors.divider)
                        Text(breakdown(measured))
                            .foregroundStyle(Theme.Colors.textSecondary)
                            .lineLimit(1)
                            .truncationMode(.tail)
                    }

                    Spacer(minLength: 0)
                }
                .font(Theme.Typography.caption)
                .padding(.horizontal, Theme.Space.md)
                .padding(.top, Theme.Space.sm)
                .help("What these jobs are projected to take out of the battery across "
                    + "eight hours asleep, from what each has actually cost on battery.")
            }

            // What the asterisk in the column heading points at, and what
            // goguma has actually done. Eight hours is a choice, and a number
            // whose basis is not stated is a number nobody can argue with.
            Text(footnoteText)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textTertiary)
                .lineLimit(1)
                .truncationMode(.tail)
                .padding(.horizontal, Theme.Space.md)
                .padding(.top, measured.isEmpty ? Theme.Space.sm : 0)
                .padding(.bottom, Theme.Space.sm)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// "goguma has woken your Mac for 4 runs, and missed 1 while asleep",
    /// or nothing at all before it has done either.
    private var scoreboard: String? {
        let woken = store.jobs.reduce(0) { $0 + $1.stats.woken }
        let slept = store.jobs.reduce(0) { $0 + $1.stats.slept }
        guard woken > 0 || slept > 0 else { return nil }

        // Saved leads, the same way the popover and `goguma list` now do.
        // What someone wants from this line is how much they got back.
        if woken > 0 {
            var line = "\(Format.count(woken, "run")) saved with goguma"
            if slept > 0 {
                line += ", \(Format.count(slept, "run")) still missed while asleep"
            }
            return line
        }
        return "\(Format.count(slept, "run")) missed while asleep so far"
    }

    private var footnoteText: String {
        let note = "* Per sleep assumes 8 hours asleep, from what each job has "
            + "actually cost on battery."
        guard let scoreboard else { return note }
        return note + "  " + scoreboard
    }

    private func breakdown(_ jobs: [JobView]) -> String {
        let shown = jobs.prefix(3).map {
            "\($0.job.displayName) " + String(format: "%.1f%%", $0.nightlyBatteryPct)
        }
        var text = shown.joined(separator: " · ")
        if jobs.count > shown.count {
            let rest = jobs.dropFirst(shown.count).reduce(0) { $0 + $1.nightlyBatteryPct }
            text += " · " + Format.count(jobs.count - shown.count, "other")
                + " " + String(format: "%.1f%%", rest)
        }
        return text
    }

    // MARK: - List

    /// Sections appear only once something is filed.
    ///
    /// A single "Ungrouped" header above every job on a machine that has never
    /// used groups is a header that says nothing, and it costs a row of vertical
    /// space plus the implication that the user has failed to organise something.
    /// Group headings are rows, not `Section` headers.
    ///
    /// A real `Section` on macOS reserves so much space around its header that
    /// the headings were taller than the jobs they introduced, and three of nine
    /// rows fell off screen, which inverts the point of grouping. `List` on
    /// macOS also has no `listSectionSpacing`, so there is no way to tune it.
    /// A header row costs one line and is styled here rather than by AppKit.
    private var jobList: some View {
        listBody
            // Behind the rows, pinned to the floor of the list.
            //
            // Most people have a handful of jobs and this window has a 320pt
            // minimum, so the usual state is a short list above a large blank
            // rectangle. `IceSceneFooter` measures what is actually left and
            // draws into it, or draws nothing when the rows fill the space.
            .background(alignment: .bottom) {
                IceSceneFooter(rowCount: rows.count)
            }
    }

    private var listBody: some View {
        List(rows, selection: $selection) { item in
            switch item {
            case let .heading(name, count):
                GroupHeader(
                    title: name.isEmpty ? "Ungrouped" : name,
                    count: count,
                    isExpanded: !isCollapsed(name)
                ) {
                    withAnimation(Theme.motion(reduced: reduceMotion)) {
                        toggleCollapsed(name)
                    }
                }
                .padding(.top, Theme.Space.xs)
                .selectionDisabled()
            case let .job(view):
                row(view)
            }
        }
        .listStyle(.inset)
        .scrollContentBackground(.hidden)
        // `List` inserts its own top inset, which sat under the column
        // headings as a second gap on top of the one the headings already
        // had. The headings are not part of the list, so the list must not
        // reserve room as if they were.
        .contentMargins(.top, 0, for: .scrollContent)
        // The selected row is drawn in the system accent by default, which is
        // whatever blue the user has set in System Settings, a saturated slab
        // that has nothing to do with this palette and shouts far louder than
        // "this is the row you are looking at" needs to. `brandFill` is the
        // same tone the toggles use, so a selected row reads as part of the
        // app rather than as a system alert dropped into it.
        .tint(Theme.Colors.brandFill)
    }

    /// One flat sequence of headings and jobs, so `List` lays them out as
    /// uniform rows.
    private enum Row: Identifiable, Hashable {
        case heading(String, Int)
        case job(JobView)

        var id: String {
            switch self {
            case let .heading(name, _): "heading:" + name
            case let .job(view): view.id
            }
        }
    }

    private var rows: [Row] {
        let groups = filteredGroups
        // Ungrouped jobs with no groups in play need no heading; a single
        // "Ungrouped" above every row tells the user nothing.
        guard store.usesGroups else { return groups.flatMap { $0.jobs.map(Row.job) } }
        return groups.flatMap { section -> [Row] in
            let head = [Row.heading(section.group, section.jobs.count)]
            return isCollapsed(section.group) ? head : head + section.jobs.map(Row.job)
        }
    }

    /// Groups with the search applied. A search that matches nothing in a group
    /// removes the group entirely rather than leaving an empty heading.
    private var filteredGroups: [(group: String, jobs: [JobView])] {
        let needle = query.trimmingCharacters(in: .whitespaces).lowercased()
        return store.groupedJobs.compactMap { section in
            guard !needle.isEmpty else { return section }
            let kept = section.jobs.filter {
                $0.job.displayName.lowercased().contains(needle)
                    || $0.job.group.lowercased().contains(needle)
                    || $0.scheduleShortText.lowercased().contains(needle)
            }
            return kept.isEmpty ? nil : (group: section.group, jobs: kept)
        }
    }

    private var searchField: some View {
        HStack(spacing: Theme.Space.xs) {
            Image(systemName: Theme.Icon.search)
                .font(Theme.Typography.iconInline)
                .foregroundStyle(Theme.Colors.textTertiary)
            TextField("Filter jobs", text: $query)
                .textFieldStyle(.plain)
                .font(Theme.Typography.body)
                .focused($queryFocused)
                // ⌘A while typing here selects what was typed.
                //
                // The window's `List` claims ⌘A for select-all-rows, and it
                // wins even while the field has the keyboard, so the shortcut
                // did nothing visible and the only way to clear a filter was to
                // hold backspace. Handling it on the field, and only while the
                // field is focused, gives the text back its own ⌘A and leaves
                // the list's alone for when the list is what you are in.
                .onKeyPress(keys: ["a"], phases: .down) { press in
                    guard press.modifiers.contains(.command) else { return .ignored }
                    NSApp.sendAction(#selector(NSText.selectAll(_:)), to: nil, from: nil)
                    return .handled
                }
            if !query.isEmpty {
                Button {
                    query = ""
                } label: {
                    Image(systemName: Theme.Icon.clear)
                        .font(Theme.Typography.iconInline)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Theme.Colors.textTertiary)
                .accessibilityLabel("Clear the filter")
            }
        }
        .padding(.horizontal, Theme.Space.md)
        .padding(.vertical, Theme.Space.xs)
    }

    // MARK: - Collapsed groups

    private var collapsed: Set<String> {
        Set(collapsedRaw.split(separator: "\n").map(String.init))
    }

    private func isCollapsed(_ group: String) -> Bool {
        // A search collapses nothing: hiding a match behind a fold the user
        // cannot see would make the filter look broken.
        query.isEmpty && collapsed.contains(group)
    }

    private func toggleCollapsed(_ group: String) {
        var next = collapsed
        if next.contains(group) { next.remove(group) } else { next.insert(group) }
        collapsedRaw = next.sorted().joined(separator: "\n")
    }

    private func row(_ view: JobView) -> some View {
        JobRow(view: view, now: store.now, store: store,
               showsManagedBadge: hasMixedSources)
            .background(
                hoveredID == view.id && selection != view.id
                    ? Theme.Colors.divider.opacity(0.45)
                    : Color.clear,
                in: RoundedRectangle(cornerRadius: Theme.Radius.row)
            )
            .onHover { inside in
                if inside {
                    hoveredID = view.id
                } else if hoveredID == view.id {
                    hoveredID = nil
                }
            }
            // No `.tag()`.
            //
            // `List(rows, selection:)` over `Identifiable` data already keys
            // selection on each element's `id`. An explicit tag on top of that
            // is a second, competing identity for the same row, and the two
            // disagreeing is why clicking a job set nothing: the detail pane
            // kept showing "select a job" while a row looked selected.
            .contentShape(Rectangle())
            // No double-click shortcut.
            //
            // Double-clicking a row opened the History *window*, which is a
            // long way from what a click on a job looks like it should do: it
            // replaced the screen instead of explaining the row, and a click
            // that lands a hair too fast did it by accident. History is still
            // one click away, from the button in the detail pane and from the
            // context menu, where it is labelled.
            .contextMenu {
                Button("Edit\u{2026}") { editing = .edit(view.job) }
                Button("History\u{2026}") { coordinator.showHistory(forJobID: view.job.id) }
                Divider()
                groupMenu(for: view)
                Divider()
                Button("Remove\u{2026}", role: .destructive) { pendingRemoval = view }
            }
    }

    /// Filing a job, from the row it belongs to.
    ///
    /// A submenu rather than a field in the edit sheet: organising a list is
    /// something you do to several jobs in a row, and making each one a modal
    /// round trip is how a feature goes unused.
    @ViewBuilder
    private func groupMenu(for view: JobView) -> some View {
        Menu("Move to Group") {
            ForEach(store.groups.filter { $0 != view.job.group }, id: \.self) { name in
                Button(name) { move(view, to: name) }
            }
            if !store.groups.filter({ $0 != view.job.group }).isEmpty { Divider() }
            Button("New Group\u{2026}") { renaming = view }
            if !view.job.group.isEmpty {
                Button("Remove from \u{201C}\(view.job.group)\u{201D}") { move(view, to: "") }
            }
        }
    }

    private func move(_ view: JobView, to group: String) {
        var edited = view.job
        edited.group = group
        // Bound as a `let` before the closure: `perform` runs its work
        // concurrently, and a captured `var` is not sendable.
        let job = edited
        let message = group.isEmpty
            ? "Removed \(job.displayName) from its group."
            : "Moved \(job.displayName) to \(group)."
        store.perform(message) { try await $0.putJob(job) }
    }

    // MARK: - Detail pane

    @ViewBuilder
    private var detailPane: some View {
        if let view = selectedJob {
            // No `ScrollView`, and no fixed height.
            //
            // A scroll view has no height of its own; it takes whatever it is
            // offered, so `.frame(height:)` around one did not mean "up to
            // this tall", it meant "always this tall". Every job whose details
            // were shorter than the pane got the difference as blank material
            // across the bottom of the window. The content here is bounded (a
            // two-line command, four stats, a handful of warnings), so it can
            // simply be as tall as it is.
            // Tight on purpose. This pane is bounded content at the foot of a
            // 472pt window, and at `md` padding with `sm` spacing it took 124
            // of those points, a quarter of the window, to show four numbers
            // and a command.
            //
            // It also costs more than its own height. The ice scene behind the
            // list draws into whatever gap the rows leave and gives up below
            // 96pt, so the pane was not merely large, it was silently taking
            // the illustration with it: selecting a job made the window fuller
            // and emptier at the same time. Horizontal padding stays at `md`
            // because that edge aligns with the list above it.
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    detailHeader(view)

                    statRow(view)

                    ForEach(view.rowWarnings, id: \.self) { reason in
                        Label(Format.noWidow(reason), systemImage: Theme.Icon.warning)
                            .font(Theme.Typography.caption)
                            .foregroundStyle(Theme.Colors.warning)
                            .fixedSize(horizontal: false, vertical: true)
                    }

                }
                .padding(.horizontal, Theme.Space.md)
                .padding(.vertical, Theme.Space.sm)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .background(.thinMaterial)
        } else {
            // Nothing selected: nothing here.
            //
            // There used to be a permanent strip across the bottom saying
            // "click a job". It was a band of material the height of its own
            // padding, present in every screenshot of this window, and it
            // explained an interaction the rows can now make themselves; they
            // highlight under the pointer. A hint that costs a permanent
            // fixture is more expensive than the confusion it removes.
            //
            // A failing job is different: that is news, not instruction, so it
            // still gets a line.
            if !failingJobs.isEmpty {
                Label(
                    failingJobs.count == 1
                        ? "1 job failed its last run: \(failingJobs[0].job.displayName)"
                        : "\(failingJobs.count) jobs failed their last run",
                    systemImage: Theme.Icon.warning
                )
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.danger)
                .padding(Theme.Space.md)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.thinMaterial)
            }
        }
    }

    private var soonestJob: JobView? {
        store.sortedJobs.first { $0.job.enabled && $0.nextFire.meaningful != nil }
    }

    private var failingJobs: [JobView] {
        store.jobs.filter { $0.runStatus == .failed }
    }

    private func detailHeader(_ view: JobView) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            Text(view.job.displayName)
                .font(Theme.Typography.headline)
                .foregroundStyle(Theme.Colors.textPrimary)
                .lineLimit(1)
                .truncationMode(.middle)

            if view.job.managed { ManagedBadge() }
            DetectionBadge(mode: view.job.detection)
            Text(view.job.source.isEmpty ? "manual" : view.job.source)
                .themeBadge(Theme.Colors.textSecondary)

            // On the title row rather than on one of its own.
            //
            // A line to itself cost the pane about 16pt on every job, and this
            // row already runs a third of its width empty between the badges
            // and the buttons. It also belongs here: the command is what the
            // job *is*, so reading it next to the name is better than reading
            // it underneath. Long ones truncate and stay selectable, which is
            // what a second line was buying.
            if !view.job.command.isEmpty {
                Text(view.job.command)
                    .font(Theme.Typography.code)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .textSelection(.enabled)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .layoutPriority(-1)
            }

            Spacer(minLength: Theme.Space.md)

            Button("Edit…") { editing = .edit(view.job) }
            Button("History…") { coordinator.showHistory(forJobID: view.job.id) }
            Button("Remove…", role: .destructive) { pendingRemoval = view }
                .disabled(store.isPerformingAction)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
    }

    /// The numbers, as a row of labelled figures rather than a column of
    /// label/value pairs.
    ///
    /// Only the ones that mean something for this job: the failure and
    /// detection counters are structurally zero for a wake-only job, and
    /// printing four zeroes implies they were measured.
    private func statRow(_ view: JobView) -> some View {
        HStack(alignment: .top, spacing: Theme.Space.lg) {
            stat("Schedule", view.scheduleText)
            stat("Runs", "\(view.stats.runs)")

            if view.hasMeasuredRuntime {
                stat("Typical", view.stats.typical.displayOrNil ?? Format.empty)
                stat(
                    "Slow run", view.stats.p95.displayOrNil ?? Format.empty,
                    help: "How long all but the slowest run took. The window is built\nfrom this rather than the maximum, so one stall doesn't widen every run after it."
                )
                stat(
                    "Ceiling", view.stats.ceiling.displayOrNil ?? Format.empty,
                    note: view.ceilingReason
                )
                stat(
                    "Failures", "\(view.stats.failures)",
                    tint: view.stats.failures > 0 ? Theme.Colors.danger : nil
                )
                stat(
                    "Missed", "\(view.stats.neverDetected)",
                    tint: view.stats.neverDetected > 0 ? Theme.Colors.danger : nil
                )
            } else {
                stat(
                    "Hold window", view.stats.ceiling.displayOrNil ?? Format.empty,
                    help: "How long the Mac is kept awake for this job.",
                    note: view.ceilingReason
                )
            }

            stat(
                "Battery / run",
                view.stats.batteryPerRunText ?? "none",
                tint: view.stats.batteryPerRun > 0 ? Theme.Colors.warning : nil,
                help: "What one firing of this job costs, averaged over the runs that "
                    + "happened on battery. Per run rather than a total, because a total "
                    + "only climbs and climbs fastest for the job that fires most often, "
                    + "which is the opposite of the comparison worth making."
            )
            Spacer(minLength: 0)
        }
    }

    private func stat(
        _ label: String, _ value: String, tint: Color? = nil, help: String? = nil,
        note: String? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Text(label)
                .font(Theme.Typography.sectionLabel)
                .foregroundStyle(Theme.Colors.textTertiary)
            // Beside the number it explains, not under it.
            //
            // It began as a line below the whole row, which put "p95 of 5 runs
            // x1.2" beneath the schedule, two columns from the hold window it
            // describes, reading as a stray note about whichever value happened
            // to sit above it. Moving it into this column fixed that and cost a
            // third line, which every stat then paid for whether it had a note
            // or not: one footnote set the height of the entire row, and the
            // row sets the height of the pane.
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                Text(value)
                    .font(Theme.Typography.body)
                    .foregroundStyle(tint ?? Theme.Colors.textPrimary)
                    .lineLimit(1)
                if let note, !note.isEmpty {
                    Text(note)
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textTertiary)
                        .lineLimit(1)
                }
            }
        }
        .fixedSize(horizontal: true, vertical: false)
        // A four-word column heading cannot carry its own definition, and
        // "Wasted" in particular is a number nobody can act on without knowing
        // what it counts.
        .help(help ?? "")
    }
}

// MARK: - Row

/// One job: on/off, what it is, when it next runs, and whether it is healthy.
///
/// One line with real columns, not two lines of prose.
///
/// The first version stacked the name over "cadence · in 9h 30m" and left the
/// right 60% of a 900pt window empty, which both wasted the space and meant
/// only six of nine jobs fitted. Cadence and next-run are short, fixed-shape
/// values, exactly what columns are for. Aligning them makes the list scannable
/// down a column instead of readable row by row, and every job fits.
private struct JobRow: View {
    let view: JobView
    let now: Date
    let store: StatusStore
    /// Whether to show the "adopted automatically" mark.
    ///
    /// False when every job is adopted, which is the common case on a machine
    /// with a watched scheduler. A badge on all nine rows distinguishes
    /// nothing and just adds nine glyphs of noise; the detail pane still says
    /// so for the selected job.
    let showsManagedBadge: Bool

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            Toggle(
                isOn: Binding(
                    get: { view.job.enabled },
                    set: { enabled in
                        store.perform(
                            enabled
                                ? "Enabled \(view.job.displayName)."
                                : "Disabled \(view.job.displayName)."
                        ) { client in
                            try await client.setJobEnabled(ref: view.job.id, enabled: enabled)
                        }
                    }
                )
            ) { EmptyView() }
                .toggleStyle(WGToggleStyle())
                .labelsHidden()
                .disabled(store.isPerformingAction)
                .accessibilityLabel("Enable \(view.job.displayName)")

            // Answers a different question from the toggle beside it: that one
            // is "should this run", this one is "did it work".
            RunStatusDot(status: view.runStatus)

            Text(view.job.displayName)
                .font(Theme.Typography.rowLabel)
                .foregroundStyle(
                    view.job.enabled ? Theme.Colors.textPrimary : Theme.Colors.textTertiary
                )
                .lineLimit(1)
                .truncationMode(.middle)
                .help(view.job.displayName)

            if showsManagedBadge, view.job.managed { ManagedBadge() }

            // The name takes whatever is left; the columns keep their width so
            // they stay aligned down the list.
            Spacer(minLength: Theme.Space.md)

            Text(view.scheduleShortText)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(width: 116, alignment: .leading)
                .help(view.scheduleText)

            // Monospaced and right-aligned: these are counting values that
            // change under the eye, and a proportional face makes a column of
            // them jitter and misalign.
            // Tabular figures so the column aligns down the list rather than
            // ragging, and weighted up once a run is close, the one row that
            // is about to happen should be findable without reading every line.
            Text(nextRunText)
                .font(isSoon ? Theme.Typography.tabularSmallEmphasised
                             : Theme.Typography.tabularSmall)
                .foregroundStyle(countdownTint)
                .lineLimit(1)
                .frame(width: 84, alignment: .trailing)

            // What this job costs a sleeping Mac overnight.
            //
            // Per night rather than per run, because per run is not the
            // comparison anyone needs: a 2% job that fires once and a 0.4% job
            // that fires sixteen times are the same number in a "per run"
            // column and eight times apart in what they do to a battery.
            Text(view.perRunCostText)
                .font(Theme.Typography.tabularSmall)
                .foregroundStyle(
                    view.hasMeasuredCost ? Theme.Colors.textSecondary : Theme.Colors.textTertiary
                )
                .lineLimit(1)
                .frame(width: 72, alignment: .trailing)
                .help(view.hasMeasuredCost
                    ? "One firing costs this much battery."
                    : "This job hasn't run on battery yet.")

            Text(view.nightlyCostText)
                .font(Theme.Typography.tabularSmall)
                .foregroundStyle(
                    view.nightlyCostIsHeavy
                        ? Theme.Colors.warning
                        : (view.hasMeasuredCost
                            ? Theme.Colors.textSecondary
                            : Theme.Colors.textTertiary)
                )
                .lineLimit(1)
                .frame(width: 80, alignment: .trailing)
                .help(view.nightlyCostHelp)

            // A fixed-width slot, so a row with a warning does not shift every
            // column to its left relative to the rows without one.
            ZStack {
                if let severity = view.worstSeverity {
                    SeverityBadge(severity: severity, reasons: view.rowWarnings)
                } else if view.holding {
                    Image(systemName: Theme.Icon.holding)
                        .font(Theme.Typography.iconRow)
                        .foregroundStyle(Theme.Colors.stateHolding)
                        .help("Holding sleep off right now")
                }
            }
            .frame(width: Theme.IconSize.hero)
        }
        .padding(.vertical, Theme.Space.xxs)
    }

    private var nextRunText: String {
        guard view.job.enabled else { return "off" }
        guard let fire = view.nextFire.meaningful else { return Format.empty }
        return Format.gap(fire, from: now)
    }

    /// Within the hour. Chosen because it is the point at which the answer to
    /// "will my Mac be awake for this" stops being hypothetical.
    private var isSoon: Bool {
        guard view.job.enabled, let fire = view.nextFire.meaningful else { return false }
        let delta = fire.timeIntervalSince(now)
        return delta > 0 && delta <= 3600
    }

    private var countdownTint: Color {
        guard view.job.enabled else { return Theme.Colors.textTertiary }
        return isSoon ? Theme.Colors.textPrimary : Theme.Colors.textSecondary
    }
}
