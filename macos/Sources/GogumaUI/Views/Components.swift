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
        VStack(spacing: Theme.Space.md) {
            if compact {
                Image(systemName: iconName)
                    .font(Theme.Typography.iconHero)
                    .foregroundStyle(tint)
            } else {
                // Nothing is happening and there is nothing to do about it from
                // here, so the space goes to the scene rather than to a larger
                // glyph. See `IceScene` for why it is this scene.
                IceScene()
                    .frame(height: Theme.Surface.sceneHeight)
                    .frame(maxWidth: .infinity)
            }

            Text(Format.noWidow(error?.errorDescription ?? "goguma isn't running."))
                .font(Theme.Typography.headline)
                .foregroundStyle(Theme.Colors.textPrimary)
                .multilineTextAlignment(.center)

            if let hint = error?.recoveryHint, !hint.isEmpty {
                Text(Format.noWidow(hint))
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .multilineTextAlignment(.center)
                    .textSelection(.enabled)
            } else if error == nil {
                Text("Run `goguma install` in Terminal to set it up.")
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                    .multilineTextAlignment(.center)
                    .textSelection(.enabled)
            }

            if let retry {
                Button("Try Again", systemImage: Theme.Icon.refresh, action: retry)
                    .buttonStyle(.bordered)
            }
        }
        .padding(Theme.Space.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
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

/// A neutral "nothing here yet" panel.
struct EmptyStateView: View {
    let icon: String
    let title: String
    var detail: String?
    /// Whether there is room for the scene. False in narrow or short panes,
    /// where a squashed illustration would look like a rendering fault.
    var showsScene: Bool = true

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
            Text(title)
                .font(Theme.Typography.headline)
                .foregroundStyle(Theme.Colors.textSecondary)
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
    let onChange: (WGDuration) -> Void

    var body: some View {
        Picker("", selection: selection) {
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
        case .unknown: Theme.Colors.textSecondary
        }
    }
}

/// A row-level warning indicator with its reasons in the tooltip.
struct SeverityBadge: View {
    let severity: RowSeverity
    let reasons: [String]

    var body: some View {
        Image(systemName: severity == .info ? Theme.Icon.info : Theme.Icon.warning)
            .font(Theme.Typography.iconRow)
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
struct ActionMessageView: View {
    let message: StatusStore.ActionMessage

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            Image(systemName: message.isError ? Theme.Icon.error : Theme.Icon.ok)
                .font(Theme.Typography.iconInline)
            Text(Format.noWidow(message.text))
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
        }
        .font(Theme.Typography.caption)
        .foregroundStyle(message.isError ? Theme.Colors.danger : Theme.Colors.textSecondary)
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


/// The state indicator, shown wherever the app says what it is doing.
///
/// The bear for idle and holding, standard symbols for the three exceptional
/// states, the same split the menu bar makes, so the popover header and the
/// menu bar glyph are never two different pictures of one state.
struct StateGlyph: View {
    let state: GogumaState
    var size: CGFloat = Theme.IconSize.row

    var body: some View {
        Group {
            switch state {
            case .idle:
                bear(asleep: true)
            case .holding:
                bear(asleep: false)
            case .paused, .cutout, .disconnected:
                Image(systemName: state.iconName)
                    .font(.system(size: size))
            }
        }
        // The bear takes the mark's own colour, never the muted state tint.
        //
        // `state.tint` resolves to a grey for idle, and a grey bear on a light
        // surface reads as black, which is exactly what this mark is not
        // allowed to be. The exceptional states keep their severity colour,
        // because there the colour is the message.
        .foregroundStyle(Theme.Colors.sweetPotato ? Color.primary : tint)
        .accessibilityHidden(true)
    }

    private var tint: Color {
        switch state {
        // `brandFill`, not `accent`. The accent is tuned for text and lines,
        // where it has to clear 4.5:1, as a large filled glyph that reads as
        // heavy and dark. This mark is a fill, which is exactly what
        // `brandFill` exists for: present and unmistakably the brand, sitting
        // only a little darker than the surface it is on.
        //
        // Ember when holding, for the same reason: it is a fill, and `ember` is
        // the lit-hearth counterpart to `brandFill`'s cold one. This is the
        // mark that sits beside "staying awake", so it should agree with the
        // counter next to it rather than stay blue while the counter glows.
        case .idle: Theme.Colors.brandFill
        case .holding: Theme.Colors.emberFill
        case .cutout: Theme.Colors.danger
        case .paused, .disconnected: Theme.Colors.textSecondary
        }
    }

    @ViewBuilder
    private func bear(asleep: Bool) -> some View {
        if Theme.Colors.sweetPotato {
            // `.original`, not `.template`.
            //
            // Template rendering throws away every colour in an image and
            // refills the silhouette with the tint, which for a colour emoji
            // means discarding the yellow cut face that is the only reason it
            // reads as a sweet potato rather than as a purple pebble. The
            // whole point of using Apple's artwork is its own palette, so it
            // must be drawn as-is and left untinted.
            Image(nsImage: SweetPotatoMark.image(size: size, asleep: asleep))
                .renderingMode(.original)
        } else {
            Image(nsImage: MenuBarMark.image(size: size, asleep: asleep))
                .renderingMode(.template)
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
