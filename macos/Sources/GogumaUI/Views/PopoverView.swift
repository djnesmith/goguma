import AppKit
import SwiftUI

/// The menu bar popover: what goguma is doing right now, what it will do
/// next, and the four things a user might want to do about it.
struct PopoverView: View {
    @Bindable var store: StatusStore
    let coordinator: WindowCoordinator

    /// Honoured for every transition in this view. See `Theme.motion(reduced:)`.
    @Environment(\.accessibilityReduceMotion) private var reduceMotion



    var body: some View {
        // Header and footer are pinned; everything between them scrolls.
        //
        // The popover has genuinely variable height: warnings are multi-line
        // and there may be none or several, and the job list grows with the
        // machine. An earlier version put the whole stack in a
        // `.frame(maxHeight:)`, which defaults to CENTRE alignment, so once
        // the content passed that height SwiftUI centred the overflow and cut
        // off the top and bottom equally. The header simply vanished off the
        // top of the screen, with no scroll bar to suggest anything was
        // missing. Pinning the ends and scrolling the middle means nothing is
        // ever unreachable, and the state line is always the first thing seen.
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding(.horizontal, Theme.Space.md)
                // `sm`, not `md`. The popover window contributes its own inset
                // above this, so 16 here measured as 24pt of air over the
                // wordmark: a band of nothing before the app has said anything.
                .padding(.top, Theme.Space.sm)
                // `xs` below. The panel that follows brings its own padding, and
                // two `sm` either side of that seam stacked into a gap wider
                // than the space between any two lines inside the panel.
                .padding(.bottom, Theme.Space.xs)

            if store.connection.blocksContent {
                // Sized by its content, not to a constant.
                //
                // A fixed height taller than what is in it, centred, splits the
                // surplus above and below, which is where the band of nothing
                // under the state line came from. The panel is short enough
                // that the popover does not need it bounded.
                DaemonUnavailableView(error: store.connection.error, compact: true) {
                    Task { await store.refresh() }
                }
                .padding(.horizontal, Theme.Space.md)
            } else {
                ThemeHairline()
                Group {
                    // Hairlines and space rather than boxes. Each rule marks a
                    // change of subject: what is happening now, what happens
                    // next, the machine, problems, the jobs themselves.
                    VStack(alignment: .leading, spacing: Theme.Space.sm) {
                        scheduleSection
                        if !store.warnings.isEmpty {
                            ThemeHairline()
                            WarningsSection(warnings: store.warnings)
                        }
                        ThemeHairline()
                        jobsSection
                    }
                    .padding(.horizontal, Theme.Space.md)
                    .padding(.vertical, Theme.Space.sm)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }

                // Outside the scroll region. These are the popover's actions;
                // if the job list is long enough to scroll, they must not be
                // the thing that scrolls out of reach.
                //
                // A beat above them: they are a change of subject from "what is
                // scheduled" to "do something about it", and collapsed they
                // would otherwise sit directly under the Jobs row.
                controls
                    .padding(.horizontal, Theme.Space.md)
                    .padding(.top, Theme.Space.xs)
                    .padding(.bottom, Theme.Space.sm)
            }

            if let message = store.actionMessage {
                ActionMessageView(message: message)
                    .padding(.horizontal, Theme.Space.md)
                    .padding(.bottom, Theme.Space.sm)
            }

            ThemeHairline()
            footer
                .padding(.horizontal, Theme.Space.md)
                .padding(.vertical, Theme.Space.sm)
        }
        .frame(width: Theme.Surface.popoverWidth)
        .themeSurface()
        .animation(Theme.motion(reduced: reduceMotion), value: store.state)
        .animation(Theme.motion(reduced: reduceMotion), value: store.warnings.count)
        .pollsDaemon(store, as: .popover)
    }

    // MARK: - Header

    /// The state line, and the counter underneath it rather than beside it.
    ///
    /// The counter used to sit to the right of the title on the same row, with
    /// a `Spacer(minLength: 0)` between them and no line limit on the title. At
    /// 20pt in a 340pt popover, "holding for morning-briefing" is wider than the
    /// space left over, so the two ran straight into each other, and because
    /// the spacer was allowed to collapse to nothing, they touched rather than
    /// wrapping. Giving the title the full width and putting the elapsed time on
    /// the detail line removes the competition instead of tuning it, and it is
    /// the better hierarchy anyway: the title says what is happening, the
    /// counter is a detail of it.
    private var header: some View {
        // No mark on the title row. The wordmark carries the name on its own,
        // and every arrangement that put a glyph up there cost something: left
        // of the title indented "goguma" while the lines below stayed flush,
        // and right of it stranded the mark between the wordmark and the
        // author link. The mark now rides with the state line instead, where
        // it has a short piece of text to sit against rather than a gap.
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                // Centred, not baseline-aligned.
                //
                // This row is two items now that the mark moved to the state
                // line, and they are 20pt and 11pt. A shared baseline is
                // correct typography and reads as a mistake at that ratio: the
                // small text's mass ends up well below the large text's, so
                // the credit looked like it had slipped down the row.
                HStack(alignment: .center, spacing: Theme.Space.sm) {
                    Text("goguma")
                        .font(Theme.Typography.popoverTitle)
                        .foregroundStyle(Theme.Colors.heading)
                        .themeHeading()
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: Theme.Space.sm)
                    authorLink
                }

                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                    VStack(alignment: .leading, spacing: 2) {
                        // The state, demoted from the title but still the first
                        // thing under it and the only line here that changes
                        // colour, so it stays the thing the eye lands on.
                        //
                        // The severity symbol rides with this line rather than in
                        // the title row, so the symbol's colour and the wording it
                        // describes arrive together instead of a red mark up beside
                        // the brand and its explanation a line below.
                        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
                            StateIcon(state: store.state, size: Theme.IconSize.row)
                            Text(Format.noWidow(headline))
                                .font(Theme.Typography.headline)
                                .foregroundStyle(stateColour)
                                .themeHeading()
                                .lineLimit(2)
                                .fixedSize(horizontal: false, vertical: true)
                            // Trailing the state, and sized against it rather
                            // than the title: 16pt beside 15pt text, where 18
                            // was drawn to sit beside the 20pt wordmark.
                            //
                            // Centred on this line's cap height for the same
                            // reason the title's guide existed — baseline
                            // alignment parks a round glyph low — but measured
                            // off `Size.emphasised`, since that is the type it
                            // now sits next to.
                            BrandGlyph(size: Self.stateMarkSize)
                                .alignmentGuide(.firstTextBaseline) { d in
                                    d.height / 2
                                        + Theme.Typography.Size.emphasised
                                        * Theme.Typography.capHeightRatio
                                }

                            // How much is riding on it.
                            //
                            // "on watch" says goguma is doing its job and
                            // nothing about the size of that job, so a Mac
                            // covering nine schedules read exactly like one
                            // covering none. The count is the whole reason the
                            // state matters, and it belongs on the line that
                            // states it rather than a card below.
                            //
                            // In caption grey at caption size, so it reads as
                            // the scale of the state rather than competing with
                            // it. Hidden when there is nothing to count, which
                            // is a different situation and already has its own
                            // wording.
                            if let scale = watchCount {
                                Text(scale)
                                    .font(Theme.Typography.caption)
                                    .foregroundStyle(Theme.Colors.textSecondary)
                                    .fixedSize()
                            }
                        }

                        if let detail = subheadline {
                            Text(Format.noWidow(detail))
                                .font(Theme.Typography.caption)
                                .foregroundStyle(Theme.Colors.textSecondary)
                                .themeProse()
                                // The caveat lives here rather than on the line.
                                //
                                // "it sleeps anyway if the Mac gets hot or the
                                // battery runs low" is worth saying and does not
                                // fit: the column takes about 42 characters and
                                // the sentence is 77, so it set as two lines
                                // under a one-line headline and made the top of
                                // the popover look like a paragraph. The line
                                // says when the hold ends, which is the thing
                                // being asked; the rest is here, and on the
                                // Safety tab, which is where somebody goes when
                                // they want to change it.
                                .help(subheadlineHelp ?? detail)
                        }
                    }
                    Spacer(minLength: Theme.Space.sm)
                    if let hold = store.status?.primaryHold, store.state == .holding {
                        Text(holdCounter(hold))
                            .font(Theme.Typography.counter)
                            .foregroundStyle(Theme.Colors.stateHolding)
                            // Never compressed or wrapped: this is the one value
                            // that ticks, and a counter that reflows as its digits
                            // change is worse than one that pushes the prose.
                            .fixedSize()
                            .accessibilityLabel(holdCounterLabel(hold))
                    }
                }
        }
    }

    /// The mark beside the state line. Smaller than the old title glyph because
    /// the text it sits against is smaller: 15pt here against the title's 20.
    private static let stateMarkSize: CGFloat = 16

    /// A job hold counts up; you want to know how long it has been running.
    /// A manual hold counts down, because the user chose the end.
    private func holdCounter(_ hold: Hold) -> String {
        if hold.isManual, let until = store.status?.keepAwakeUntil.meaningful {
            return WGDuration(seconds: max(0, until.timeIntervalSince(store.now))).clockString
        }
        return hold.elapsed(at: store.now).clockString
    }

    private func holdCounterLabel(_ hold: Hold) -> String {
        if hold.isManual, let until = store.status?.keepAwakeUntil.meaningful {
            let left = WGDuration(seconds: max(0, until.timeIntervalSince(store.now)))
            return "Staying awake for another \(left.displayString)"
        }
        return "Held for \(hold.elapsed(at: store.now).displayString)"
    }

    /// Lowercase on purpose. The voice is plain and calm, and a 20pt sentence
    /// case title in a menu bar popover reads as an announcement; this reads as
    /// a status. Job names are lowercase slugs already, so "holding for
    /// morning-briefing" stays consistent either way.
    private var headline: String {
        switch store.state {
        case .disconnected: "goguma isn't running"
        case .paused: "paused"
        case .cutout: store.status?.cutout?.kind.label ?? "let the Mac sleep"
        case .holding:
            if let hold = store.status?.primaryHold, hold.isManual {
                // Not "holding for manual keep-awake"; the user asked for
                // this, so it should read back as the thing they asked for.
                "staying awake"
            } else if let hold = store.status?.primaryHold {
                "holding for \(hold.displayName)"
            } else {
                "holding sleep off"
            }
        // Not "idle". That word is on screen 99% of the time and it reads as
        // "doing nothing" (which sounds like off, or broken) when in fact
        // the app is armed, watching every registered job, and usually has a
        // wake already booked. It described the internal state machine rather
        // than the user's situation, and it undersold the product hardest at
        // exactly the moment it was most often read.
        case .idle:
            if store.status?.starting == true {
                // The first second after launch, before anything is measured.
                // "nothing to wake for" on a Mac with nine jobs reads as a
                // fault rather than as a daemon that has not looked yet.
                "starting up"
            } else if !store.wakeSuppressed.isEmpty {
                // Its own headline, and a short one.
                //
                // This fell through to "nothing to wake for", which is both
                // wrong and too long: there are jobs, and the wake is being
                // withheld on purpose. At 20pt it wrapped to two lines, and
                // because the mark and the runs-saved count trail the first
                // line, the result read "nothing to 🍠 · 3 runs saved with
                // goguma / wake for".
                "wake held back"
            } else {
                store.nextWake == nil ? "nothing to wake for" : "on watch"
            }
        }
    }

    /// What the current state is worth, in the plainest number available.
    ///
    /// Two different claims, and which one is true depends on how long goguma
    /// has been running:
    ///
    /// - Once it has woken the Mac for anything, that count is the only honest
    ///   thing the tool can say about its own worth. Every one of those runs
    ///   was going to be skipped. `goguma list` reports the same number as
    ///   "woken for N runs that would have been missed"; this is the short
    ///   form of that sentence, not a second metric.
    /// - Before then there is no score, so it falls back to how much is
    ///   riding on it. "on watch" says goguma is doing its job and nothing
    ///   about the size of that job, so a Mac covering nine schedules read
    ///   exactly like one covering none.
    ///
    /// Only while idle. Holding already names the job in the headline and
    /// counts the rest in the subheadline; paused and disconnected are about
    /// goguma rather than about the jobs; a cutout is about the machine.
    private var watchCount: String? {
        guard store.state == .idle, store.status?.starting != true else { return nil }
        // Not while a wake is being withheld. The headline there is reporting
        // something the user may want to act on, and trailing it with a count
        // of past wins both lengthens the line past one row and answers a
        // question nobody is asking at that moment.
        guard store.wakeSuppressed.isEmpty else { return nil }
        let saved = store.jobs.reduce(0) { $0 + $1.stats.woken }
        if saved > 0 {
            // "with goguma" only where the line has room for it.
            //
            // Beside "on watch" the whole thing fits on one row and the
            // attribution is worth having. Beside "nothing to wake for" it
            // pushes the headline onto a second line, and because this count
            // trails the *first* line the result reads as one scrambled
            // sentence. The word "goguma" is directly above it either way.
            let long = store.nextWake == nil
            return long
                ? "· \(Format.count(saved, "run")) saved"
                : "· \(Format.count(saved, "run")) saved with goguma"
        }
        let watching = store.jobs.filter(\.job.enabled).count
        guard watching > 0 else { return nil }
        return "· \(Format.count(watching, "job"))"
    }

    /// The longer version of `subheadline`, shown on hover.
    ///
    /// Only where there is something to add. Everything else falls back to the
    /// line itself, which is what a tooltip should say when the text it is
    /// attached to has been truncated.
    private var subheadlineHelp: String? {
        guard store.state == .holding else { return nil }
        return "goguma lets the Mac sleep anyway if it gets too hot or the battery "
            + "runs low. Those limits are on the Safety tab in Settings."
    }

    private var subheadline: String? {
        switch store.state {
        case .disconnected:
            // Nothing. The panel directly below says what is wrong, what setup
            // will do, and carries the button that does it; repeating any of
            // that here printed "goguma isn't running" twice on a 340pt
            // surface, once above the other.
            return nil
        case .paused:
            return "No wakes will be scheduled and no holds taken until you resume."
        case .cutout:
            guard let cutout = store.status?.cutout else { return nil }
            var text = cutout.detail.isEmpty ? "Holds were force-released." : cutout.detail
            if cutout.releasedHolds > 0 {
                text += " Released \(Format.count(cutout.releasedHolds, "hold"))."
            }
            if cutout.latched { text += " Latched until conditions improve." }
            return text
        case .holding:
            guard let hold = store.status?.primaryHold else { return nil }
            // A manual hold has no job to detect, so every phrase below is
            // false for it: "waiting for the job to appear" describes a job
            // that does not exist and will never arrive.
            if hold.isManual {
                guard let until = store.status?.keepAwakeUntil.meaningful else {
                    return "You asked for this."
                }
                return "Until \(Format.clock(until))."
            }
            // A wrapped command is running by definition: goguma started it.
            // It is not waiting to appear, it was never going to be found in
            // the process table, and its deadline is a renewing lease rather
            // than a ceiling worth counting down.
            if hold.isWrappedCommand {
                return "Until it finishes."
            }
            var parts: [String] = []
            // Wake-only jobs are never observed, so "waiting for the job to
            // appear" is a state they can never leave, the same objection
            // that applies to a manual hold above.
            if hold.detection == .wakeOnly {
                parts.append("Not watched, holding the full window")
            } else {
                parts.append(hold.detected ? "Job detected" : "Waiting for the job to appear")
            }
            if let remaining = hold.remaining(at: store.now), remaining.seconds > 0 {
                parts.append("ceiling in \(remaining.displayString)")
            }
            if store.status?.holds.count ?? 0 > 1 {
                parts.append("+\((store.status?.holds.count ?? 1) - 1) more")
            }
            return parts.joined(separator: " · ")
        case .idle:
            if store.status?.sleepBlocked == true {
                return "Sleep is still blocked, releasing shortly."
            }
            if store.status?.starting == true {
                return "Checking the machine and working out the next wake."
            }
            // Nothing. "on watch" above and the wake time on the card below
            // already say the machine can sleep and when it will be woken, so a
            // sentence restating both sat between them saying nothing new.
            //
            // The other idle returns stay: each of them answers a question the
            // rest of the surface does not. An empty job list in particular
            // reads as a fault without its line.
            if store.nextWake != nil {
                return nil
            }
            // Deliberately does NOT repeat store.wakeSuppressed.
            //
            // The schedule card below renders that reason in full, and the
            // daemon writes it as a whole explanatory sentence: at 20% battery
            // it is thirty words about thresholds. Returning it here printed
            // the identical paragraph twice, a few points apart, on a 340pt
            // surface. The header says the situation, the card says why.
            // Nothing. The card immediately below carries the daemon's own
            // sentence, so "Nothing is scheduled. See below for why." was a
            // line whose entire content was a pointer to the next line.
            if !store.wakeSuppressed.isEmpty {
                return nil
            }
            return store.jobs.isEmpty ? "Looking for scheduled jobs…" : nil
        }
    }

    // MARK: - Schedule

    /// Three distinct outcomes, never conflated:
    ///   - a wake is scheduled → when, and how long until;
    ///   - a wake is deliberately withheld → the daemon's own reason, phrased
    ///     as information (a genuine failure comes through `warnings` instead);
    ///   - nothing to wake for at all.
    @ViewBuilder
    private var scheduleSection: some View {
        if let wake = store.nextWake {
            // The wake time alone.
            //
            // This used to name the job it was waking for and the minute that
            // job fires. Both are true and neither was being asked: the
            // question a menu bar popover answers is "when does this thing next
            // do something", and the job list two sections below already says
            // which job that is. Naming it here made the card three lines to
            // deliver one fact.
            KeyValueRow(
                label: "Next wake",
                value: "\(Format.dayAndTime(wake)) · \(Format.until(wake, from: store.now))",
                icon: Theme.Icon.nextWake,
                // One accent per surface, and while holding it is the counter.
                //
                // The ember counter and an accented wake time are two things
                // competing to be looked at first. When a hold is running, the
                // fact in front of the user is how long it lasts; the next wake
                // is still there, just no longer shouting over it. Idle, there
                // is no counter and the wake time takes the accent back.
                tint: store.state == .holding ? Theme.Colors.textPrimary : Theme.Colors.accent,
                monospaced: false
            )
            .themeCard()
        } else if !store.wakeSuppressed.isEmpty {
            // The reason, beside its icon, and nothing else.
            //
            // This had a "Next wake held back" row above the reason, which is
            // now the headline four points higher up. Three restatements of
            // one fact stacked vertically, and the row's own height plus the
            // stack's spacing is where the card's excess whitespace came from.
            HStack(alignment: .top, spacing: Theme.Space.sm) {
                Image(systemName: Theme.Icon.wakeSuppressed)
                    .font(Theme.Typography.iconInline)
                    .foregroundStyle(Theme.Colors.stateSuppressed)
                    .frame(width: Theme.IconSize.row)
                    .accessibilityHidden(true)
                // Verbatim: the daemon already writes this as user-facing prose,
                // and paraphrasing would lose the specific numbers that make it
                // make sense.
                Text(Format.noWidow(store.wakeSuppressed))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
            }
            .themeCard()
            .accessibilityElement(children: .combine)
            .accessibilityLabel("Next wake held back. \(store.wakeSuppressed)")
        } else if !store.noWakeReason.isEmpty {
            // A reason, not just an absence.
            //
            // "none scheduled" reads identically whether there are no jobs, all
            // of them are switched off, or a schedule cannot be parsed, and
            // those need three different things done about them. The daemon
            // knows which it is, so it says.
            HStack(alignment: .top, spacing: Theme.Space.sm) {
                Image(systemName: Theme.Icon.nextWake)
                    .font(Theme.Typography.iconInline)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .frame(width: Theme.IconSize.row)
                    .accessibilityHidden(true)
                Text(Format.noWidow(store.noWakeReason))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
            }
            .themeCard()
            .accessibilityElement(children: .combine)
            .accessibilityLabel("No wake scheduled. \(store.noWakeReason)")
        } else {
            KeyValueRow(
                label: "Next wake",
                value: "none scheduled",
                icon: Theme.Icon.nextWake,
                tint: Theme.Colors.textSecondary
            )
            .themeCard()
        }
    }

    // MARK: - Jobs

    /// How many jobs the popover lists before deferring to the Jobs window.
    /// Enough to cover a normal machine without the popover growing unbounded.
    /// Ten, so a normal schedule fits in one glance.
    ///
    /// The list is the only part of the popover that grows, and `MenuBarExtra`
    /// grows upward from a fixed bottom edge, which is why this was once as
    /// low as three. That constraint is gone: the window is a vendored
    /// `FluidMenuBarExtra` that recomputes its frame from the status item and
    /// extends *downward*, so length costs screen space rather than clipping
    /// the header off the top.
    ///
    /// Ten because most people have fewer, and paging to another window to see
    /// two more jobs is worse than a slightly taller dropdown. Past ten it
    /// caps and offers "Show all N jobs", which is a real browsing surface
    /// rather than a longer list.
    ///
    /// Ten rows is roughly 660pt of popover against ~875pt of usable height
    /// below the menu bar, so it still fits a 900pt screen with the header,
    /// controls and footer.
    private static let jobsShownInPopover = 10

    private var visibleJobs: [JobView] {
        Array(store.sortedJobs.prefix(Self.jobsShownInPopover))
    }

    /// The same capped set, but keeping each job under its own heading.
    ///
    /// Capping the flat list first and then bucketing (rather than taking N
    /// from each group) keeps "the next six things that will happen" as the
    /// selection rule, which is what this surface is for. Groups organise what
    /// is shown; they do not change what gets shown.
    private var visibleGroups: [(group: String, jobs: [JobView])] {
        let shown = Set(visibleJobs.map(\.id))
        return store.groupedJobs.compactMap { section in
            let kept = section.jobs.filter { shown.contains($0.id) }
            return kept.isEmpty ? nil : (group: section.group, jobs: kept)
        }
    }

    /// Remembered across popover opens, so the popover reopens the way it was
    /// left rather than resetting to collapsed every time.
    @AppStorage("popover.jobsExpanded") private var jobsExpanded = false

    /// Collapsed by default, and it stays however the user last left it.
    ///
    /// It was a disclosure, and the disclosure was the problem. `MenuBarExtra`
    /// keeps its bottom edge fixed when it resizes, so expanding pushed the top
    /// of the window up past the menu bar and cut it off; pinning the height to
    /// stop that left a band of empty space whenever the list was shut. Every
    /// arrangement traded one of those faults for the other.
    ///
    /// Three rows is small enough that hiding them was never buying much
    /// quiet, and showing them always means the window is one fixed size,
    /// filled. No resize to mis-anchor, and no gap to explain. The rest of the
    /// list is one click away in a window built for it.
    @ViewBuilder
    private var jobsSection: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            jobsHeader

            // Always in the view tree, collapsed to zero height.
            //
            // Built lazily behind `if jobsExpanded`, the first expansion was
            // also the first time these rows had ever been constructed and
            // measured, so it did extra work at exactly the moment the window
            // was resizing, and only ever misbehaved the first time. Keeping
            // them built means expanding is a frame change and nothing else.
            //
            // Cheap to hold: nine light rows whose data the store is already
            // publishing for the menu bar glyph.
            jobsList
                .frame(height: jobsExpanded ? nil : 0, alignment: .top)
                .clipped()
                .allowsHitTesting(jobsExpanded)
                .accessibilityHidden(!jobsExpanded)
        }
    }

    @ViewBuilder
    private var jobsList: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            if store.jobs.isEmpty {
                Text(store.jobs.isEmpty && !store.connection.blocksContent
                    ? "Looking for scheduled jobs on this Mac. Anything found appears here within a couple of minutes."
                    : "No jobs registered. Add one from the Jobs window, or import your crontab with `goguma import`.")
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, Theme.Space.sm)
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    if store.usesGroups {
                        ForEach(visibleGroups, id: \.group) { section in
                            Text(section.group.isEmpty ? "Ungrouped" : section.group)
                                .font(Theme.Typography.sectionLabel)
                                .foregroundStyle(Theme.Colors.textTertiary)
                                .padding(.top, Theme.Space.xs)
                                .padding(.leading, Theme.Space.sm)
                            ForEach(section.jobs) { compactJobRow($0) }
                        }
                    } else {
                        ForEach(visibleJobs) { compactJobRow($0) }
                    }
                }

                if store.jobs.count > Self.jobsShownInPopover {
                    Button { coordinator.showJobs() } label: {
                        Text("Show all \(store.jobs.count) jobs")
                            .font(Theme.Typography.caption)
                    }
                    .buttonStyle(FooterButtonStyle())
                    .pointingHand()
                    .padding(.top, Theme.Space.xs)
                    .padding(.leading, Theme.Space.sm)
                }
            }
        }
    }

    private var jobsHeader: some View {
        Button {
            withAnimation(Theme.motion(reduced: reduceMotion)) { jobsExpanded.toggle() }
        } label: {
            HStack(spacing: Theme.Space.xs) {
                Image(systemName: Theme.Icon.disclosure)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textTertiary)
                    .rotationEffect(.degrees(jobsExpanded ? 90 : 0))
                Text("Jobs (\(store.jobs.count))")
                    .font(Theme.Typography.sectionLabel)
                    .foregroundStyle(Theme.Colors.textTertiary)
                Spacer(minLength: Theme.Space.sm)
                if let severity = worstJobSeverity {
                    SeverityBadge(severity: severity, reasons: jobWarnings)
                }
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Jobs, \(store.jobs.count)")
        .accessibilityHint(jobsExpanded ? "Collapses the job list" : "Expands the job list")
    }

    /// The most severe thing wrong with any job, so collapsing cannot hide it.
    private var worstJobSeverity: RowSeverity? {
        store.jobs.compactMap(\.worstSeverity).max()
    }

    private var jobWarnings: [String] {
        store.jobs.flatMap(\.rowWarnings)
    }


    private func compactJobRow(_ view: JobView) -> some View {
        HStack(spacing: Theme.Space.sm) {
            Toggle(
                isOn: Binding(
                    get: { view.job.enabled },
                    set: { enabled in
                        store.perform(enabled ? "Enabled \(view.job.displayName)" : "Disabled \(view.job.displayName)") { client in
                            try await client.setJobEnabled(ref: view.job.id, enabled: enabled)
                        }
                    }
                )
            ) {
                EmptyView()
            }
            .toggleStyle(WGToggleStyle())
            .labelsHidden()
            .disabled(store.isPerformingAction)
            .accessibilityLabel("Enable \(view.job.displayName)")

            VStack(alignment: .leading, spacing: 0) {
                Text(view.job.displayName)
                    .font(Theme.Typography.caption)
                    .foregroundStyle(
                        view.job.enabled ? Theme.Colors.textPrimary : Theme.Colors.textTertiary
                    )
                    .lineLimit(1)
                Text(nextRunText(view))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textTertiary)
                    .lineLimit(1)
            }

            Spacer(minLength: 0)

            if let severity = view.worstSeverity {
                SeverityBadge(severity: severity, reasons: view.rowWarnings)
            }
            if view.holding {
                Image(systemName: Theme.Icon.holding)
                    .font(Theme.Typography.iconInline)
                    .foregroundStyle(Theme.Colors.stateHolding)
                    .accessibilityLabel("Currently holding")
            }
        }
        .themeRow()
        // Not tappable.
        //
        // This row used to open the History *window* on a single click. The
        // popover is a glance surface: a click on a line of information should
        // not replace the screen with a different window, least of all one the
        // user did not ask for and cannot get back from without closing it.
        // The row already says everything it has (cadence, next run, health)
        // and the toggle on it still works. "Show all N jobs" is the deliberate
        // way out, and History lives one level in from there, labelled.
    }

    /// The short cadence, not the full schedule.
    ///
    /// This row is 340pt wide and already carries the time until the next run,
    /// so "twice daily, at 09:00 and 21:00 · in 30m" both repeated itself and
    /// truncated. The Jobs window learned this first; the popover is narrower
    /// and needed it more.
    private func nextRunText(_ view: JobView) -> String {
        guard view.job.enabled else { return "Disabled" }
        guard let fire = view.nextFire.meaningful else { return view.scheduleShortText }
        return "\(view.scheduleShortText) · \(Format.until(fire, from: store.now))"
    }

    // MARK: - Controls

    /// Four actions in a 2×2 grid, paired by opposite.
    ///
    /// Keep Awake used to sit on a line of its own above a row of three, which
    /// left it visibly orphaned, a single control floating with nothing beside
    /// it reads as something that did not fit rather than something deliberate.
    /// It is also the natural opposite of Sleep Now, so pairing them puts the
    /// two halves of "hold sleep off / let it sleep" side by side, with the two
    /// quieting actions beneath.
    ///
    /// While a manual hold is running, its cell becomes the way to stop it and
    /// says how long is left. The control and its state occupy one slot rather
    /// than the state appearing somewhere else on the surface.
    private var controls: some View {
        Grid(horizontalSpacing: Theme.Space.xs, verticalSpacing: Theme.Space.xs) {
            GridRow {
                keepAwakeControl
                Button {
                    store.perform("Released every hold.") { try await $0.sleepNow() }
                } label: {
                    Label("Sleep Now", systemImage: Theme.Icon.sleepNow)
                        .frame(maxWidth: .infinity)
                }
                .disabled(store.isPerformingAction || !(store.status?.holding ?? false))
                .help("Release every open hold so the Mac can sleep immediately.")
            }
            GridRow {
                Button {
                    store.perform("Skipping the next wake.") { try await $0.skipNext() }
                } label: {
                    Label("Skip Wake", systemImage: Theme.Icon.skipNext)
                        .frame(maxWidth: .infinity)
                }
                .disabled(store.isPerformingAction || store.nextWake == nil)
                .help("Cancel the next scheduled wake. The job after it is unaffected.")

                Button {
                    let paused = store.status?.paused ?? false
                    store.perform(paused ? "Resumed." : "Paused.") { client in
                        if paused { try await client.resume() } else { try await client.pause() }
                    }
                } label: {
                    Label(
                        store.status?.paused == true ? "Resume" : "Pause",
                        systemImage: store.status?.paused == true
                            ? Theme.Icon.resume : Theme.Icon.pause
                    )
                    .frame(maxWidth: .infinity)
                }
                .disabled(store.isPerformingAction)
                .help("Stop scheduling wakes and taking holds until resumed.")
            }
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .labelStyle(.titleAndIcon)
    }

    /// One cell: start a manual hold, or stop the one that is running.
    @ViewBuilder
    private var keepAwakeControl: some View {
        if let until = store.status?.keepAwakeUntil.meaningful {
            Button {
                store.perform("Released the manual hold.") { client in
                    _ = try await client.keepAwake(for: .zero)
                }
            } label: {
                Label(
                    "Stop · \(Format.gap(until, from: store.now))",
                    systemImage: Theme.Icon.keepAwake
                )
                .frame(maxWidth: .infinity)
            }
            .disabled(store.isPerformingAction)
            .help("End the manual hold and let the Mac sleep normally.")
        } else {
            // A plain Button with a real AppKit menu, not SwiftUI's `Menu`.
            //
            // A `Menu` on macOS sizes to its own content and ignores every
            // attempt to widen it: `.frame(maxWidth: .infinity)` on the label,
            // on the menu itself, `.menuStyle(.button)`, and an explicit
            // `.frame(width:)` were all tried, and the one control that opened a
            // menu stayed visibly narrower than the three beside it. A Button
            // fills its grid cell by construction, and `NSMenu.popUp` gives the
            // same list without the layout fight.
            Button {
                showKeepAwakeMenu()
            } label: {
                Label("Keep Awake", systemImage: Theme.Icon.keepAwake)
                    .frame(maxWidth: .infinity)
            }
            .disabled(store.isPerformingAction || store.connection.blocksContent)
            .help(
                "Hold sleep off for a fixed time whether or not a job is running. "
                    + "It still sleeps if the Mac gets hot or the battery runs low."
            )
        }
    }

    /// Pops the duration list at the pointer.
    private func showKeepAwakeMenu() {
        let menu = NSMenu()
        for choice in Self.keepAwakeChoices {
            let item = NSMenuItem(
                title: choice.displayString, action: nil, keyEquivalent: ""
            )
            item.target = KeepAwakeMenuTarget.shared
            item.action = #selector(KeepAwakeMenuTarget.pick(_:))
            item.representedObject = choice.seconds
            menu.addItem(item)
        }
        KeepAwakeMenuTarget.shared.onPick = { seconds in
            keepAwake(WGDuration(seconds: seconds))
        }
        menu.popUp(positioning: nil, at: NSEvent.mouseLocation, in: nil)
    }

    /// The daemon clamps to 1 minute - 12 hours; these sit inside that.
    private static let keepAwakeChoices: [WGDuration] = [
        900, 1800, 3600, 7200, 14400,
    ].map { WGDuration(seconds: $0) }

    private func keepAwake(_ duration: WGDuration) {
        store.performReporting { client in
            let response = try await client.keepAwake(for: duration)
            guard let until = response.until?.date else { return "Staying awake." }
            let suffix = response.clamped ? " (adjusted to the allowed range)" : ""
            return "Awake until \(Format.clock(until))" + suffix
        }
    }

    // MARK: - Brand

    /// The credit, on the title's row.
    ///
    /// It had a row of its own above the title, which left a near-empty band
    /// across the top of the popover: one short string, right-aligned, over an
    /// otherwise blank line. Beside the name it costs no vertical space and
    /// reads as a byline, which is what it is.
    private var authorLink: some View {
        Group {
            Link(destination: URL(string: Self.authorURL)!) {
                HStack(spacing: 2) {
                    Text(Self.authorLabel)
                    Image(systemName: "arrow.up.right")
                        .font(Theme.Typography.iconInline)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(FooterButtonStyle(underlined: true))
            .help("Open Jun Nam's LinkedIn profile")
            .accessibilityLabel("Open the profile of Jun Nam, who made goguma")
        }
        .font(Theme.Typography.caption)

    }

    // Author rather than project: the popover already says goguma on the left,
    // so repeating the repository path here spends the widest row on the page
    // saying the same word twice. Swap to the site when there is one.
    /// The state's own colour, so the demoted line still reads as the live
    /// value rather than as a second subtitle.
    private var stateColour: Color {
        switch store.state {
        case .holding: Theme.Colors.stateHolding
        case .cutout: Theme.Colors.stateCutout
        case .paused: Theme.Colors.statePaused
        case .disconnected: Theme.Colors.textSecondary
        case .idle: Theme.Colors.heading
        }
    }

    /// The author, not the project.
    ///
    /// The title directly below already says goguma, so pointing this at the
    /// repository would spend the widest row on the surface saying the same
    /// word twice. Someone who wants the source has the title, the README and
    /// the release they installed from; this row is for the person behind it.
    private static let authorURL = "https://www.linkedin.com/in/jun-nam-4ba16b326/"
    private static let authorLabel = "made by Jun Nam"

    // MARK: - Footer

    /// Quiet navigation, not links.
    ///
    /// These were `.buttonStyle(.link)`, which renders as blue underlined-ish
    /// text, web chrome in a Mac popover, and three saturated blue items
    /// competing with the one accented value the surface is actually about.
    /// They are navigation to somewhere else, which is the least important
    /// thing here, so they read as quiet secondary text and brighten on hover.
    private var footer: some View {
        HStack(spacing: Theme.Space.md) {
            footerButton("Jobs", icon: Theme.Icon.jobs) { coordinator.showJobs() }
                .pointingHand()
            footerButton("Settings", icon: Theme.Icon.settings) { coordinator.showSettings() }
                .pointingHand()
            Spacer(minLength: Theme.Space.sm)
            temperature
            footerButton("Quit", icon: Theme.Icon.quit) { coordinator.quit() }
                .pointingHand()
        }
    }

    /// CPU temperature, when the machine reports one.
    ///
    /// Here rather than in the status block because it is context, not state:
    /// the number only becomes interesting as it approaches the thermal cutout,
    /// and putting it in the footer means it is available without competing
    /// with what goguma is currently doing.
    ///
    /// Omitted entirely when the platform has no reading, rather than shown as
    /// a dash. A machine with no sensor is not a machine at 0°C, and the
    /// thermal cutout treats an unavailable sensor as unverifiable rather than
    /// as cool.
    @ViewBuilder
    private var temperature: some View {
        if let celsius = store.status?.power.cpuTempC,
            let text = Format.temperature(celsius)
        {
            HStack(spacing: Theme.Space.xs) {
                Image(systemName: "thermometer.medium")
                    .font(Theme.Typography.iconInline)
                Text(text)
                    .font(Theme.Typography.caption)
                    .monospacedDigit()
            }
            // Warm once it is within ten degrees of the cutout, so the colour
            // arrives before the release does rather than reporting it.
            .foregroundStyle(
                celsius >= Self.warmThresholdC
                    ? Theme.Colors.warning : Theme.Colors.textTertiary
            )
            .help("CPU temperature. Holds are released above the thermal cutout.")
            .accessibilityLabel("CPU temperature \(text)")
        }
    }

    /// Ten degrees below the daemon's default 80°C thermal cutout.
    private static let warmThresholdC: Double = 70

    private func footerButton(
        _ title: String, icon: String, action: @escaping () -> Void
    ) -> some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.xs) {
                Image(systemName: icon)
                    .font(Theme.Typography.iconInline)
                Text(title)
                    .font(Theme.Typography.caption)
            }
            // The whole label is the target, not just the glyphs, a 4pt gap
            // between icon and text that does nothing on click is the kind of
            // detail that makes an interface feel unreliable.
            .contentShape(Rectangle())
        }
        .buttonStyle(FooterButtonStyle())
        .help(title)
    }
}

/// Secondary text that lifts to primary on hover and dims while pressed.
///
/// `underlinesOnHover` is for the one item that leaves the app. Everything else
/// in the footer navigates within it, where an underline would be web chrome in
/// a Mac popover; a link out is the exception, because the underline is what
/// says "this opens a browser" before you click it.
private struct FooterButtonStyle: ButtonStyle {
    /// Draw the underline at rest, not only under the pointer.
    ///
    /// Hover-only underlining is discoverable by people who were already going
    /// to click. For the credit in the title row, which is the one link on the
    /// surface and sits among plain labels, the rule has to be there before
    /// the pointer is.
    var underlined = false

    @State private var hovering = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(
                hovering ? Theme.Colors.textPrimary : Theme.Colors.textSecondary
            )
            .underline(underlined)
            .opacity(configuration.isPressed ? 0.55 : 1)
            .contentShape(Rectangle())
            .onHover { isHovering in
                // Eased rather than snapped: the colour lift and the rule
                // arriving together at speed reads as a flicker on a small
                // target, and this one is four words wide.
                withAnimation(.easeOut(duration: 0.12)) { hovering = isHovering }
            }
    }
}

/// Receives clicks from the keep-awake `NSMenu`.
///
/// `NSMenuItem` needs an Objective-C target, which a SwiftUI view cannot be.
/// A single shared object keeps the bridge to one place rather than one
/// retained controller per menu.
@MainActor
final class KeepAwakeMenuTarget: NSObject {
    static let shared = KeepAwakeMenuTarget()
    var onPick: ((Double) -> Void)?

    @objc func pick(_ sender: NSMenuItem) {
        guard let seconds = sender.representedObject as? Double else { return }
        onPick?(seconds)
    }
}
