import SwiftUI

// MARK: - Empty / error states

/// The state every window shows when the daemon can't be reached.
///
/// This is load-bearing: goguma's daemon is a separate process that may
/// simply not be installed, so "not running" is a normal condition, not a
/// crash, and each surface has to say so plainly and offer the fix.
struct DaemonUnavailableView: View {
    let error: DaemonError?
    /// Set in the popover, which gives this view 150pt and no room for a scene.
    var compact: Bool = false
    var retry: (() -> Void)?

    var body: some View {
        VStack(spacing: compact ? Theme.Space.sm : Theme.Space.md) {
            if compact {
                // Only when nothing else fills the panel. With the setup
                // disclosure present this was a large decorative mark above
                // three lines that each say something.
                if !Onboarding.canSelfInstall {
                    Image(systemName: iconName)
                        .font(Theme.Typography.iconHero)
                        .foregroundStyle(tint)
                }
            } else {
                // Nothing is happening and there is nothing to do about it from
                // here, so the space goes to the scene rather than to a larger
                // glyph. See `IceScene` for why it is this scene.
                IceScene()
                    .frame(height: Theme.Surface.sceneHeight)
                    .frame(maxWidth: .infinity)
            }

            // Only where nothing above has said it. `compact` is the popover,
            // whose header carries the state line two rows up, so repeating it
            // printed "goguma isn't running" twice on a 340pt surface.
            if !compact {
                Text(Format.noWidow(error?.errorDescription ?? "goguma isn't running."))
                    .font(Theme.Typography.headline)
                    .foregroundStyle(Theme.Colors.textPrimary)
                    .multilineTextAlignment(.center)
            }

            if let hint = error?.recoveryHint, !hint.isEmpty {
                Text(Format.noWidow(hint))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .multilineTextAlignment(.center)
                    // Wraps rather than truncating. In the popover this sits in
                    // a fixed 150pt panel, which trimmed the sentence to an
                    // ellipsis mid-clause and lost the half that mattered.
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            } else if error == nil {
                // Onboarding, not a literal: a downloaded app has no `goguma`
                // on the PATH, so naming the command is advice its reader
                // cannot act on.
                Text(Format.noWidow(Onboarding.disconnectedAdvice))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .multilineTextAlignment(.center)
                    .textSelection(.enabled)
            }

            // What setup will do, before it is agreed to.
            //
            // Everything below the button is irreversible from the user's point
            // of view: a root helper gets installed and a scan reads their
            // crontab. Asking for a password first and explaining afterwards is
            // the wrong order, and "trust me" is not a thing software gets to
            // say about a privileged install.
            if Onboarding.canSelfInstall {
                SetupDisclosure()
            }

            // One button, in the app's own clothes.
            //
            // `.borderedProminent` is AppKit's stock blue capsule, which is the
            // one control on this surface that belongs to a different program.
            //
            // "Try Again" is gone with it. A visible popover polls the daemon
            // once a second, so the button did exactly what happens on its own
            // a second later, and offering it next to a password prompt implied
            // the password might not have taken.
            if Onboarding.canSelfInstall {
                Button { Onboarding.runInstaller() } label: {
                    Text("Set up goguma")
                        .font(Theme.Typography.rowLabel)
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(SetupButtonStyle())
            } else if let retry {
                // No installer to run, so the only useful action is to look
                // again after the user has run it themselves.
                Button("Check again", systemImage: Theme.Icon.refresh, action: retry)
                    .buttonStyle(.bordered)
            }
        }
        .padding(.horizontal, compact ? Theme.Space.md : Theme.Space.xl)
        // `xs` when compact. The header now ends on `xs` too, and the rows
        // inside carry `sm` between them, so the panel's own padding is the
        // only place left where the seam above it can be tightened.
        .padding(.vertical, compact ? Theme.Space.xs : Theme.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .center)
    }

    private var iconName: String {
        guard let error else { return Theme.Icon.disconnected }
        switch error {
        case .protocolMismatch: return Theme.Icon.warning
        case .refused, .malformedResponse: return Theme.Icon.error
        default: return Theme.Icon.disconnected
        }
    }

    private var tint: Color {
        guard let error else { return Theme.Colors.textSecondary }
        switch error {
        case .protocolMismatch: return Theme.Colors.warning
        case .refused, .malformedResponse: return Theme.Colors.danger
        default: return Theme.Colors.textSecondary
        }
    }
}

/// The one button on the setup panel, in the app's own material.
///
/// Filled with the brand rather than the system accent: this surface has a
/// palette and AppKit's blue is not in it. Sized and shaped like the popover's
/// own controls so it reads as part of the same program, and it is the only
/// control here, so it takes the full width rather than sitting in a row of
/// one.
struct SetupButtonStyle: ButtonStyle {
    @State private var hovering = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(Color.white)
            .padding(.vertical, Theme.Space.sm)
            .padding(.horizontal, Theme.Space.md)
            .background(fill(pressed: configuration.isPressed), in: Theme.cardShape)
            .contentShape(Theme.cardShape)
            .onHover { isHovering in
                withAnimation(.easeOut(duration: 0.12)) { hovering = isHovering }
            }
    }

    private func fill(pressed: Bool) -> Color {
        // Pressed is darker, hover a shade lighter, neither is a new hue.
        if pressed { return Theme.Colors.accent.opacity(0.82) }
        return hovering ? Theme.Colors.accent.opacity(0.92) : Theme.Colors.accent
    }
}

/// The three facts someone needs before installing a background service that
/// runs as root and reads their scheduler.
///
/// Deliberately not a link to a privacy policy. A policy is read by nobody at
/// the moment of consent, and the claims here are small enough to state: what
/// is read, where it goes, and what needs the password. Each one is checkable
/// against the source, which is the only reason to believe any of it.
struct SetupDisclosure: View {
    var body: some View {
        // `sm`, not `xs`. Three single-line facts set four points apart read as
        // one block of text rather than as three separate things.
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            // Literal symbols, not the app's semantic ones. `Theme.Icon.cutout`
            // is a warning triangle, which turned a plain statement of what
            // gets installed into an alert about it, and reusing the
            // disconnected glyph for "stays on this Mac" said nothing.
            // The argument, at the size of an argument.
            //
            // This was a fourth bullet, level with "no telemetry", which made
            // the one fact that justifies the whole prompt the least prominent
            // thing on the panel. Waking a sleeping Mac is privileged: no
            // amount of design removes that, so the honest move is to lead
            // with it rather than bury it in a list.
            // One line, at `rowLabel` rather than `headline`.
            //
            // 15pt needed two lines in the 276pt this panel actually has, and a
            // claim broken across two lines reads as a paragraph rather than as
            // a statement. 13pt medium holds it on one and keeps the weight
            // that makes it the loudest thing here; the scale factor is a floor
            // for a wider system font, not a design.
            Text("Waking a sleeping Mac needs root access.")
                .font(Theme.Typography.rowLabel)
                .foregroundStyle(Theme.Colors.textPrimary)
                .lineLimit(1)
                .minimumScaleFactor(0.88)
                .frame(maxWidth: .infinity, alignment: .center)

            // Two, not three.
            //
            // There were three, and the third ("installs a small helper, hence
            // the password") said again what the line above the list already
            // says: that this needs root. Three single-line claims stacked under
            // a heading read as a form to get through rather than as two things
            // worth knowing, on the one panel where somebody is deciding whether
            // to trust the thing at all. What it reads and where that stays are
            // one thought, so they are one line.
            row("key", "Installs a small helper, which is what the password is for")
            row("laptopcomputer", "Reads your scheduled jobs to know when to wake. Nothing leaves this Mac")

            // The three lines above are the summary; this is the whole account,
            // written to be checked rather than believed.
            Link(destination: Self.securityDoc) {
                Text("Read what it can and can't do")
                    .font(Theme.Typography.caption)
                    .underline()
            }
            .foregroundStyle(Theme.Colors.accent)
            // Centred under the block rather than hung off the text column.
            // It belongs to all three lines, not to the last one.
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.top, Theme.Space.xxs)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private static let securityDoc = URL(
        string: "https://github.com/junnam586/goguma/blob/main/SECURITY.md")!

    private func row(_ icon: String, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
            // A fixed column, because SF Symbols are not a fixed width. A
            // calendar, a laptop and a key are three different sizes, so each
            // line of text began at a different x and the block read as
            // ragged. `.trailing` keeps the symbols themselves on one edge too.
            Image(systemName: icon)
                .font(Theme.Typography.iconInline)
                .foregroundStyle(Theme.Colors.textTertiary)
                .frame(width: Theme.IconSize.row, alignment: .center)
                .accessibilityHidden(true)
            // One line each. Three two-line rows read as a wall of prose
            // rather than as three facts, and the scale factor is a floor
            // rather than a design: the strings are written to fit, and this
            // only catches a wider system font.
            Text(text)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
                .lineLimit(1)
                .minimumScaleFactor(0.85)
            Spacer(minLength: 0)
        }
    }
}

/// A neutral "nothing here yet" panel.
struct EmptyStateView: View {
    let icon: String
    let title: String
    var detail: String?
    /// Whether there is room for the scene. False in narrow or short panes,
    /// where a squashed illustration would look like a rendering fault.
    var showsScene: Bool = true
    /// Shows that something is still happening, for an emptiness that is
    /// temporary rather than a state of affairs.
    var working: Bool = false

    var body: some View {
        VStack(spacing: Theme.Space.sm) {
            if showsScene {
                IceScene()
                    .frame(height: Theme.Surface.sceneHeight)
                    .frame(maxWidth: .infinity)
            } else {
                Image(systemName: icon)
                    .font(Theme.Typography.iconHero)
                    .foregroundStyle(Theme.Colors.textTertiary)
            }
            HStack(spacing: Theme.Space.xs) {
                if working {
                    ProgressView()
                        .controlSize(.small)
                        .scaleEffect(0.7)
                }
                Text(title)
                    .font(Theme.Typography.headline)
                    .foregroundStyle(Theme.Colors.textSecondary)
            }
            if let detail {
                Text(Format.noWidow(detail))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textTertiary)
                    .multilineTextAlignment(.center)
            }
        }
        .padding(Theme.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - Warnings

/// One actionable daemon warning. The `fix` string is literally the command to
/// run, so it is rendered as selectable code rather than prose.
struct WarningRow: View {
    let warning: DaemonWarning

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.sm) {
            Image(systemName: warning.kind.isCritical ? Theme.Icon.cutout : Theme.Icon.warning)
                .font(Theme.Typography.iconRow)
                .foregroundStyle(tint)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(Format.noWidow(warning.message))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textPrimary)
                    .fixedSize(horizontal: false, vertical: true)

                if !warning.fix.isEmpty {
                    // The fix is the point of the warning, so it is a control
                    // rather than a footnote.
                    //
                    // It used to be muted grey text: the command was there,
                    // but acting on it meant reading it, remembering it, and
                    // retyping it into a terminal, which is enough friction
                    // that people live with the problem instead. One click puts
                    // it on the clipboard.
                    FixCommand(command: warning.fix)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(Theme.Space.sm)
        .background(tint.opacity(0.10), in: Theme.rowShape)
    }

    private var tint: Color {
        warning.kind.isCritical ? Theme.Colors.danger : Theme.Colors.warning
    }
}

/// A stack of warnings with a heading, or nothing at all when there are none.
struct WarningsSection: View {
    let warnings: [DaemonWarning]

    /// The popover does not scroll, so every section in it has to be bounded.
    /// Warnings are the only part that could grow without limit; three is more
    /// than a calm tool should ever have at once, and the count in the heading
    /// still says how many there really are.
    private static let maxShown = 3

    var body: some View {
        if !warnings.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                SectionLabel(text: Format.count(warnings.count, "problem"))
                ForEach(warnings.prefix(Self.maxShown)) { WarningRow(warning: $0) }
                if warnings.count > Self.maxShown {
                    Text("+\(warnings.count - Self.maxShown) more; run `goguma doctor`")
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                        .padding(.leading, Theme.Space.sm)
                }
            }
        }
    }
}

// MARK: - Small building blocks

/// A quiet section heading.
///
/// Not upper-cased. All-caps labels shout, and the voice here is calm; the
/// weight and the muted colour already carry the hierarchy.
struct SectionLabel: View {
    let text: String

    var body: some View {
        Text(text)
            .font(Theme.Typography.sectionLabel)
            .foregroundStyle(Theme.Colors.textTertiary)
            .accessibilityAddTraits(.isHeader)
    }
}

/// Label on the left, value on the right, the workhorse of the popover and
/// the history detail pane.
struct KeyValueRow: View {
    let label: String
    let value: String
    var icon: String?
    var tint: Color = Theme.Colors.textPrimary
    var monospaced: Bool = false

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            if let icon {
                Image(systemName: icon)
                    .font(Theme.Typography.iconInline)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .frame(width: Theme.IconSize.row)
                    .accessibilityHidden(true)
            }
            // One line, always. A label that wraps grows the row downward and
            // pushes sideways into the value, which is how a long job name in
            // this label ran into the time on the right. Anything that needs
            // more than a line does not belong in a label/value row; use a
            // stacked layout instead.
            Text(label)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: Theme.Space.sm)
            Text(value)
                .font(monospaced ? Theme.Typography.counter : Theme.Typography.caption)
                .foregroundStyle(tint)
                .lineLimit(1)
                .truncationMode(.middle)
                // The value is the answer; the label is context for it. When
                // the two compete for a narrow row, the label gives way.
                .layoutPriority(1)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A duration setting as a menu of sensible choices.
///
/// These were free-text fields accepting forms like `1h30m`. Three problems, in
/// increasing order of seriousness: `1m30s` is an ugly thing to read in a
/// settings row; a text field gives no clue what a reasonable value would be;
/// and a typo is only discovered after Return, when the daemon rejects or
/// silently clamps it.
///
/// The real values here span a narrow, well-understood range, so a menu is
/// simply the better control, every option is valid by construction and the
/// set of options *is* the documentation.
///
/// A value set outside these presets (by the CLI, or clamped by the daemon) is
/// added to the menu rather than being silently rounded to the nearest preset.
/// Showing someone a value they did not choose is worse than an odd menu entry.
/// The menu only. Its label, and the column it aligns to, belong to the grid
/// that places it; this used to carry a `title` and a hard-coded 168pt label
/// column that every other row type had to agree with by hand.
struct DurationPicker: View {
    let presets: [WGDuration]
    let current: WGDuration
    /// What zero means, when zero is a real choice.
    ///
    /// A per-job override has a meaningful "unset": the job takes whatever the
    /// global setting or the learned value says. Without a row for it, a menu
    /// of durations offers no way back to the default once one is picked.
    var includeZeroAs: String?
    let onChange: (WGDuration) -> Void

    var body: some View {
        Picker("", selection: selection) {
            if let includeZeroAs {
                // `.tag(0.0)`, not `.tag(0)`. The selection is a Double, and
                // an Int tag matches nothing, which SwiftUI renders as a menu
                // with no title at all rather than as an error.
                Text(includeZeroAs).tag(0.0)
                Divider()
            }
            ForEach(options, id: \.seconds) { option in
                Text(option.displayString).tag(option.seconds)
            }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
    }

    /// Presets, plus the live value when it is not one of them.
    private var options: [WGDuration] {
        var all = presets
        if !current.isZero, !presets.contains(where: { $0.seconds == current.seconds }) {
            all.append(current)
            all.sort()
        }
        return all
    }

    private var selection: Binding<Double> {
        Binding(
            get: { current.seconds },
            set: { onChange(WGDuration(seconds: $0)) }
        )
    }
}

/// The same control for a whole-percent setting.
///
/// A sibling of `DurationPicker` rather than a `Stepper` or a `Slider`,
/// because the settings pane already reads as a column of menus and a lone
/// slider among them looks like a different control for a different kind of
/// value. It keeps the same "presets, plus whatever is actually set" rule, so
/// a value chosen from the command line still shows rather than snapping to
/// the nearest preset.
struct PercentPicker: View {
    let presets: [Int]
    let current: Int
    let onChange: (Int) -> Void

    var body: some View {
        Picker("", selection: selection) {
            ForEach(options, id: \.self) { pct in
                Text("\(pct)%").tag(pct)
            }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
    }

    private var options: [Int] {
        presets.contains(current) || current <= 0 ? presets : (presets + [current]).sorted()
    }

    private var selection: Binding<Int> {
        Binding(get: { current }, set: onChange)
    }
}

/// Detection mode as a compact badge.
///
/// Only `pattern` is tinted as a caution, because it is the one mode that can
/// silently fail. `wakeOnly` is neutral: it is the most common mode on a
/// machine with auto-adopted jobs, and tinting it as a problem would paint the
/// entire jobs list with false alarms.
struct DetectionBadge: View {
    let mode: DetectionMode

    var body: some View {
        HStack(spacing: Theme.Space.xxs) {
            Image(systemName: mode.iconName)
                .font(Theme.Typography.iconInline)
            Text(mode.label)
        }
        .themeBadge(tint)
        .help(mode.explanation)
    }

    private var tint: Color {
        switch mode {
        case .mark, .wakeOnly: Theme.Colors.textSecondary
        case .pattern: Theme.Colors.warning
        // Genuine version skew: this build doesn't know the mode at all.
        case .unknown: Theme.Colors.warning
        }
    }
}

/// Marks a job goguma adopted for the user, rather than one they added.
struct ManagedBadge: View {
    var body: some View {
        Image(systemName: Theme.Icon.managed)
            .font(Theme.Typography.iconInline)
            .foregroundStyle(Theme.Colors.managed)
            .help(
                "Adopted automatically from a watched scheduler. It stays in step with its source: "
                    + "it comes back on the next sync if removed, and retires on its own when the "
                    + "source entry disappears."
            )
            .accessibilityLabel("Adopted automatically")
    }
}

/// Run outcome as a compact badge.
struct OutcomeBadge: View {
    let outcome: RunOutcome

    var body: some View {
        HStack(spacing: Theme.Space.xxs) {
            Image(systemName: outcome.iconName)
                .font(Theme.Typography.iconInline)
            Text(outcome.label)
        }
        .themeBadge(tint)
    }

    private var tint: Color {
        switch outcome {
        case .ok: Theme.Colors.ok
        case .failed: Theme.Colors.danger
        case .ceiling: Theme.Colors.warning
        case .neverDetected: Theme.Colors.danger
        case .cutout: Theme.Colors.stateCutout
        // Warning, not danger. The job did not run, which matters, but nothing
        // broke: goguma decided the battery was too low to wake for it, or the
        // machine was off. Red would read as a fault to go and fix.
        case .slept: Theme.Colors.warning
        case .unknown: Theme.Colors.textSecondary
        }
    }
}

/// A row-level warning indicator with its reasons in the tooltip.
///
/// The size follows the text it sits beside: table rows read at `iconRow`,
/// the popover's caption-sized rows pass `iconInline` so the glyph does not
/// outweigh an 11pt label.
struct SeverityBadge: View {
    let severity: RowSeverity
    let reasons: [String]
    var font: Font = Theme.Typography.iconRow

    var body: some View {
        Image(systemName: severity == .info ? Theme.Icon.info : Theme.Icon.warning)
            .font(font)
            .foregroundStyle(tint)
            .help(reasons.joined(separator: "\n"))
            .accessibilityLabel(reasons.joined(separator: ". "))
    }

    private var tint: Color {
        switch severity {
        case .critical: Theme.Colors.danger
        case .warning: Theme.Colors.warning
        case .info: Theme.Colors.textSecondary
        }
    }
}

/// A brief inline confirmation or error from a user action.
///
/// A contained notice rather than loose text. This was an icon and a coloured
/// string with no background, sharing a toolbar row with a button: a long
/// message wrapped to two lines inside its 260pt slot and read as red text
/// spilled across the window rather than as anything the window was telling
/// you. Errors are exactly when a surface should look most deliberate.
///
/// One line, truncated, with the whole message on hover. A result from an
/// action is a headline; anyone who wants the sentence can have it, and a
/// toolbar is not the place to set a paragraph.
struct ActionMessageView: View {
    let message: StatusStore.ActionMessage

    var body: some View {
        let ink: Color = message.isError ? Theme.Colors.danger : Theme.Colors.ok

        return HStack(spacing: Theme.Space.xs) {
            Image(systemName: message.isError ? Theme.Icon.error : Theme.Icon.ok)
                .font(Theme.Typography.iconInline)
                .foregroundStyle(ink)
            Text(message.text)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textPrimary)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .padding(.horizontal, Theme.Space.sm)
        .padding(.vertical, Theme.Space.xs)
        .background(
            RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous)
                .fill(ink.opacity(0.10))
        )
        .overlay(
            RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous)
                .strokeBorder(ink.opacity(0.28), lineWidth: 1)
        )
        .help(message.text)
        .accessibilityLabel(message.text)
    }
}

/// A disclosure header: quiet by default, lifting on hover like the footer.
///
/// Deliberately not `.bordered`: a button frame around a section heading turns
/// a piece of structure into a control and doubles the visual weight of the
/// quietest label on the surface.
struct DisclosureHeaderStyle: ButtonStyle {
    @State private var hovering = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(
                hovering ? Theme.Colors.textSecondary : Theme.Colors.textTertiary
            )
            .opacity(configuration.isPressed ? 0.55 : 1)
            .onHover { hovering = $0 }
    }
}


/// The mark, and only the mark.
///
/// Replaces the old `StateGlyph`, which sat left of the title carrying both
/// the brand and the state and, for the two states that are on screen almost
/// all the time, carrying neither: `SweetPotatoMark.image` ignores its
/// `asleep:` argument, and the emoji is drawn `.original`, so no tint ever
/// landed on it either. One slot therefore read as a logo in `.idle` and
/// `.holding` and as a severity signal in the other three, which is why it
/// never looked settled in either position.
///
/// So the two jobs are split. This is the wordmark's companion and never
/// changes; `StateIcon` carries severity, on the line that already changes
/// colour for it.
struct BrandGlyph: View {
    var size: CGFloat = Theme.IconSize.row

    var body: some View {
        Group {
            if Theme.Colors.sweetPotato {
                // `.original`, not `.template`.
                //
                // Template rendering throws away every colour in an image and
                // refills the silhouette with the tint, which for a colour
                // emoji means discarding the yellow cut face that is the only
                // reason it reads as a sweet potato rather than as a purple
                // pebble. The whole point of using Apple's artwork is its own
                // palette, so it must be drawn as-is and left untinted.
                Image(nsImage: SweetPotatoMark.image(size: size, asleep: false))
                    .renderingMode(.original)
            } else {
                // `brandFill`, not `accent`. The accent is tuned for text and
                // lines, where it has to clear 4.5:1, and reads as heavy and
                // dark as a filled glyph. This mark is a fill, which is what
                // `brandFill` exists for: unmistakably the brand, sitting only
                // a little darker than the surface it is on.
                Image(nsImage: MenuBarMark.image(size: size, asleep: false))
                    .renderingMode(.template)
                    .foregroundStyle(Theme.Colors.brandFill)
            }
        }
        .accessibilityHidden(true)
    }
}

/// The severity symbol for the three states that have something to warn about.
///
/// Nothing at all for `.idle` and `.holding`. Those states are already fully
/// described by the headline's wording and its colour, and a symbol that is
/// present in every state stops being a signal.
struct StateIcon: View {
    let state: GogumaState
    var size: CGFloat = Theme.IconSize.row

    var body: some View {
        switch state {
        // Nothing for `.disconnected` either. The words say it, the panel
        // below explains it, and the line already carries the sweet potato;
        // a third symbol bracketing the sentence read as a badge on an error
        // rather than as the app's own name.
        case .idle, .holding, .disconnected:
            EmptyView()
        case .paused, .cutout, .disconnected:
            Image(systemName: state.iconName)
                .font(.system(size: size))
                .foregroundStyle(tint)
                .accessibilityHidden(true)
        }
    }

    private var tint: Color {
        switch state {
        case .cutout: Theme.Colors.danger
        case .paused, .disconnected: Theme.Colors.textSecondary
        // Unreachable: `body` draws nothing for these.
        case .idle, .holding: Theme.Colors.textSecondary
        }
    }
}

/// Did it work last time? A small mark, distinct from the enable toggle.
///
/// The toggle answers "should this run"; this answers "did it work". They are
/// different questions and both need answering without a click.
///
/// Never colour alone: each state has its own shape as well as its own tint,
/// so the column survives greyscale and colourblindness. `notWatched` is a
/// hollow ring rather than a filled dot precisely because it is an absence of
/// information, and it must not be mistaken for a verdict.
struct RunStatusDot: View {
    let status: RunStatus
    var size: CGFloat = 7

    var body: some View {
        Group {
            switch status {
            case .running:
                Circle().fill(Theme.Colors.stateHolding)
            case .succeeded:
                Circle().fill(Theme.Colors.ok)
            case .failed:
                // A square, not a dot. Shape carries the alarm even where hue
                // cannot.
                Theme.badgeShape.fill(Theme.Colors.danger)
            case .neverRun:
                Circle().strokeBorder(Theme.Colors.textTertiary, lineWidth: 1)
            case .notWatched:
                Circle().strokeBorder(
                    Theme.Colors.textTertiary.opacity(0.55),
                    style: StrokeStyle(lineWidth: 1, dash: [1.5, 1.5])
                )
            }
        }
        .frame(width: size, height: size)
        .help(status.label)
        .accessibilityLabel(status.label)
    }
}

/// A group heading in a list: chevron, tracked caps, and a count.
///
/// Small caps with positive tracking rather than body-weight grey. This
/// supersedes an earlier rule in `Docs/DESIGN.md` that section labels are never
/// upper-cased; that rule was written for a label sitting alone, where caps
/// read as shouting. A heading that has to be told apart from the rows beneath
/// it at a glance is the case where tracked caps earn their place, and the
/// universal principles this project follows allow them at +0.08-0.16em.
struct GroupHeader: View {
    let title: String
    let count: Int
    let isExpanded: Bool
    let toggle: () -> Void

    var body: some View {
        Button(action: toggle) {
            HStack(spacing: Theme.Space.xs) {
                Image(systemName: Theme.Icon.disclosure)
                    .font(Theme.Typography.disclosureChevron)
                    .rotationEffect(.degrees(isExpanded ? 90 : 0))
                // "BRIEFINGS (3)", not the count adrift at the far right.
                // A number a hundred points from the word it counts has to be
                // re-associated by the reader every time.
                Text("\(title.uppercased()) (\(count))")
                    .font(Theme.Typography.groupHeading)
                    .tracking(Theme.Typography.groupHeadingTracking)
                Spacer(minLength: 0)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(DisclosureHeaderStyle())
        .accessibilityLabel("\(title), \(Format.count(count, "job"))")
        .accessibilityHint(isExpanded ? "Collapse" : "Expand")
    }
}

/// A switch that keeps its colour when the window is not key.
///
/// AppKit draws every standard control in a desaturated "inactive" appearance
/// once its window loses focus, so a screen full of enabled jobs went uniformly
/// grey the moment another app was clicked, and "on" became indistinguishable
/// from "off" in exactly the situation where someone is glancing over from
/// another window to check. That is a state this app is not allowed to be
/// ambiguous about.
///
/// Drawing it directly also fixes the colour: the system switch is a saturated
/// blue that belongs to no palette here, whereas this one is the arctic accent
/// like everything else that means "on".
struct WGToggleStyle: ToggleStyle {
    private static let width: CGFloat = 26
    private static let height: CGFloat = 15

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func makeBody(configuration: Configuration) -> some View {
        Button {
            configuration.isOn.toggle()
        } label: {
            ZStack(alignment: configuration.isOn ? .trailing : .leading) {
                Capsule()
                    // `brandFill`, not `accent`.
                    //
                    // The accent is tuned for text and hairlines, where it has
                    // to clear 4.5:1, which makes it dark, and as a filled
                    // capsule 26pt wide it read as a heavy navy slab rather
                    // than a switch that is on. `brandFill` is the token for
                    // exactly this: present and unmistakably the brand, a step
                    // lighter than the surface rather than a hole in it.
                    .fill(configuration.isOn ? Theme.Colors.brandFill : Theme.Colors.divider)
                Circle()
                    .fill(Theme.Colors.card)
                    // A hairline, so the knob still reads against the pale
                    // "off" track as well as the accent "on" one.
                    .overlay(Circle().strokeBorder(Theme.Colors.divider.opacity(0.8), lineWidth: 0.5))
                    .padding(1.5)
            }
            .frame(width: Self.width, height: Self.height)
            .animation(Theme.motion(reduced: reduceMotion), value: configuration.isOn)
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityRepresentation {
            Toggle(isOn: configuration.$isOn) { configuration.label }
        }
    }
}


/// A command the user should run, with one click to take it.
struct FixCommand: View {
    let command: String

    @State private var copied = false

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            Text(command)
                .font(Theme.Typography.code)
                .foregroundStyle(Theme.Colors.textPrimary)
                .textSelection(.enabled)
                .padding(.horizontal, Theme.Space.xs)
                .padding(.vertical, Theme.Space.xxs)
                .background(Theme.Colors.card, in: Theme.badgeShape)

            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(command, forType: .string)
                copied = true
                // Reverts on its own: a button stuck reading "Copied" tells the
                // user nothing about whether a second click worked.
                Task {
                    try? await Task.sleep(for: .seconds(2))
                    copied = false
                }
            } label: {
                Label(
                    copied ? "Copied" : "Copy",
                    systemImage: copied ? Theme.Icon.ok : Theme.Icon.copy
                )
                .font(Theme.Typography.caption)
            }
            .buttonStyle(.plain)
            .foregroundStyle(copied ? Theme.Colors.ok : Theme.Colors.accent)
            .help("Copy this command, then paste it into Terminal.")
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Fix: run \(command)")
    }
}
