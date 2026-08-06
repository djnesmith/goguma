import SwiftUI

// MARK: - Sparkline

/// A duration sparkline for one job, drawn from `stats.recent`.
///
/// Hand-drawn with `Path` rather than a charting library: it has to render at
/// 72×18 inside a table cell, where a general-purpose chart's axes, padding,
/// and hit-testing are all cost with no benefit. Runs whose outcome was not
/// `ok` are marked, because a flat line of "fast" runs that were actually all
/// force-released tells the opposite of the truth.
struct DurationSparkline: View {
    let runs: [Run]
    var size: CGSize = Theme.Chart.sparklineSize

    private struct Marker: Hashable {
        var point: CGPoint
        var healthy: Bool
    }

    var body: some View {
        Group {
            if runs.count < 2 {
                // One point is not a trend. Show a baseline so the column keeps
                // its rhythm instead of collapsing.
                Path { path in
                    path.move(to: CGPoint(x: 0, y: size.height / 2))
                    path.addLine(to: CGPoint(x: size.width, y: size.height / 2))
                }
                .stroke(Theme.Colors.chartAxis, style: StrokeStyle(lineWidth: Theme.Stroke.hairline))
            } else {
                let markers = layout()
                ZStack {
                    Path { path in
                        guard let first = markers.first else { return }
                        path.move(to: first.point)
                        for marker in markers.dropFirst() { path.addLine(to: marker.point) }
                    }
                    .stroke(
                        Theme.Colors.chartSeries,
                        style: StrokeStyle(
                            lineWidth: Theme.Stroke.sparkline, lineCap: .round, lineJoin: .round
                        )
                    )

                    Path { path in
                        for marker in markers where !marker.healthy {
                            path.addEllipse(
                                in: CGRect(
                                    x: marker.point.x - Theme.Chart.sparklineDotRadius,
                                    y: marker.point.y - Theme.Chart.sparklineDotRadius,
                                    width: Theme.Chart.sparklineDotRadius * 2,
                                    height: Theme.Chart.sparklineDotRadius * 2
                                )
                            )
                        }
                    }
                    .fill(Theme.Colors.chartSeriesAlert)
                }
            }
        }
        .frame(width: size.width, height: size.height)
        .accessibilityLabel(accessibilityText)
    }

    private func layout() -> [Marker] {
        let values = runs.map(\.duration.seconds)
        let maxValue = max(values.max() ?? 0, .leastNonzeroMagnitude)
        let usableHeight = max(1, size.height - Theme.Chart.insetY * 2)
        let step = runs.count > 1 ? size.width / CGFloat(runs.count - 1) : 0

        return runs.enumerated().map { index, run in
            let fraction = maxValue > 0 ? run.duration.seconds / maxValue : 0
            let y = Theme.Chart.insetY + usableHeight * (1 - CGFloat(fraction))
            return Marker(
                point: CGPoint(x: CGFloat(index) * step, y: y),
                healthy: run.outcome.isHealthy
            )
        }
    }

    private var accessibilityText: String {
        guard !runs.isEmpty else { return "No run history" }
        let bad = runs.filter { !$0.outcome.isHealthy }.count
        let base = "\(Format.count(runs.count, "recent run"))"
        return bad == 0 ? base : "\(base), \(bad) not OK"
    }
}

// MARK: - Full run chart

/// Run durations over time with the ceiling as a reference line, plus the hold
/// duration as a second series.
///
/// Two series rather than one because the gap between them *is* the product's
/// subject: `duration` is what the job needed, `hold_duration` is what the
/// machine actually stayed awake for, and everything between the lines is
/// battery spent for nothing.
struct RunDurationChart: View {
    let runs: [Run]
    let ceiling: WGDuration

    /// False for a wake-only job. Its `duration` is always zero because nothing
    /// was ever observed, so plotting a "Job runtime" series would draw a flat
    /// line along the axis and assert something untrue. The hold windows and
    /// the fixed ceiling are the whole story for those jobs.
    var observable: Bool = true

    /// Added to `ceiling` for the reference line on a job that cannot be
    /// observed. See `referenceSeconds`.
    var wakeBuffer: WGDuration = .zero

    var height: CGFloat = Theme.Chart.historyHeight

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            legend
            if runs.isEmpty {
                EmptyStateView(
                    icon: Theme.Icon.history,
                    title: "No runs recorded yet",
                    detail: observable
                        ? "Durations appear here once this job has run at least once."
                        : "Hold windows appear here once this job's schedule has fired at least once.",
                    // Sits inside the chart's own frame, under a legend. The
                    // scene belongs to a whole empty pane, not a slot in one.
                    showsScene: false
                )
                .frame(height: height)
            } else {
                chartBody
            }
        }
    }

    private var legend: some View {
        HStack(spacing: Theme.Space.md) {
            if observable {
                LegendSwatch(color: Theme.Colors.chartSeries, label: "Job runtime")
            }
            LegendSwatch(color: Theme.Colors.chartWaste, label: "Sleep held off")
            if referenceSeconds > 0 {
                LegendSwatch(
                    color: Theme.Colors.chartReference,
                    label: observable
                        ? "Ceiling (\(ceiling.displayString))"
                        : "Expected (\(WGDuration(seconds: referenceSeconds).displayString))",
                    dashed: true
                )
            }
            Spacer(minLength: 0)
        }
    }

    private var chartBody: some View {
        HStack(alignment: .top, spacing: Theme.Space.xs) {
            axisLabels
            GeometryReader { geo in
                let plot = CGSize(
                    width: max(1, geo.size.width - Theme.Chart.insetX * 2),
                    height: max(1, geo.size.height - Theme.Chart.insetY * 2)
                )
                let scale = verticalScale
                ZStack(alignment: .topLeading) {
                    gridlines(in: plot)
                    holdSeries(in: plot, scale: scale)
                    if observable {
                        durationArea(in: plot, scale: scale)
                        durationLine(in: plot, scale: scale)
                    }
                    referenceLine(in: plot, scale: scale)
                    if observable {
                        outcomeDots(in: plot, scale: scale)
                    }
                }
                .padding(EdgeInsets(
                    top: Theme.Chart.insetY, leading: Theme.Chart.insetX,
                    bottom: Theme.Chart.insetY, trailing: Theme.Chart.insetX
                ))
            }
            .frame(height: height)
        }
        .frame(height: height)
    }

    // MARK: Scaling

    /// Top of the y axis, in seconds. Includes the ceiling so the reference
    /// line is always on screen — a chart that clips the very line it is being
    /// compared against is worse than no chart.
    private var verticalScale: Double {
        let peak = max(
            runs.map(\.duration.seconds).max() ?? 0,
            runs.map(\.holdDuration.seconds).max() ?? 0,
            referenceSeconds
        )
        return peak > 0 ? peak * 1.1 : 1
    }

    /// What the bars should be compared against.
    ///
    /// For a job that can be observed, that is the ceiling: the hold ends when
    /// the job does, so held time and the ceiling are the same measurement.
    ///
    /// For a job that cannot, it is the ceiling **plus the wake buffer**. The
    /// window opens `fire - buffer` and runs for the full ceiling after it, so
    /// held time is always buffer + ceiling — and a line drawn at the ceiling
    /// alone sat below every single bar, which reads as "overran every run" for
    /// a job doing exactly what it was configured to do. The bars did not lie;
    /// the line was measuring a different thing from them.
    private var referenceSeconds: Double {
        observable ? ceiling.seconds : ceiling.seconds + wakeBuffer.seconds
    }

    private func x(_ index: Int, in plot: CGSize) -> CGFloat {
        guard runs.count > 1 else { return plot.width / 2 }
        return plot.width * CGFloat(index) / CGFloat(runs.count - 1)
    }

    private func y(_ seconds: Double, in plot: CGSize, scale: Double) -> CGFloat {
        let fraction = scale > 0 ? min(1, max(0, seconds / scale)) : 0
        return plot.height * (1 - CGFloat(fraction))
    }

    // MARK: Layers

    private func gridlines(in plot: CGSize) -> some View {
        Path { path in
            for fraction in [0.0, 0.5, 1.0] {
                let lineY = plot.height * CGFloat(fraction)
                path.move(to: CGPoint(x: 0, y: lineY))
                path.addLine(to: CGPoint(x: plot.width, y: lineY))
            }
        }
        .stroke(Theme.Colors.chartAxis, style: StrokeStyle(lineWidth: Theme.Stroke.hairline))
    }

    /// Bars, not hairlines.
    ///
    /// These were stroked at the series line width, which is right for a line
    /// on a chart and wrong for the only mark on one. For a job that cannot be
    /// observed there is no duration area and no runtime line — the hold bars
    /// are the entire chart, and four 2pt threads spread across a wide plot
    /// read as a rendering fault rather than as data.
    ///
    /// The width follows the spacing so it works at both ends: a handful of
    /// runs get solid columns, twenty get slim ones, and neither collides.
    private func holdSeries(in plot: CGSize, scale: Double) -> some View {
        let slot = runs.count > 1
            ? plot.width / CGFloat(runs.count - 1)
            : plot.width
        let width = max(Theme.Stroke.series, min(Theme.Chart.maxBarWidth, slot * 0.42))

        return Path { path in
            for (index, run) in runs.enumerated() {
                let top = y(run.holdDuration.seconds, in: plot, scale: scale)
                // Clamped so the first and last bars sit inside the plot rather
                // than half-hanging off its edges.
                let centre = min(max(x(index, in: plot), width / 2), plot.width - width / 2)
                path.addRoundedRect(
                    in: CGRect(
                        x: centre - width / 2,
                        y: top,
                        width: width,
                        height: max(1, plot.height - top)
                    ),
                    cornerSize: CGSize(width: Theme.Radius.badge, height: Theme.Radius.badge)
                )
            }
        }
        .fill(Theme.Colors.chartWaste)
    }

    private func durationArea(in plot: CGSize, scale: Double) -> some View {
        Path { path in
            guard !runs.isEmpty else { return }
            path.move(to: CGPoint(x: x(0, in: plot), y: plot.height))
            for (index, run) in runs.enumerated() {
                path.addLine(
                    to: CGPoint(
                        x: x(index, in: plot),
                        y: y(run.duration.seconds, in: plot, scale: scale)
                    )
                )
            }
            path.addLine(to: CGPoint(x: x(runs.count - 1, in: plot), y: plot.height))
            path.closeSubpath()
        }
        .fill(Theme.Colors.chartFill)
    }

    private func durationLine(in plot: CGSize, scale: Double) -> some View {
        Path { path in
            for (index, run) in runs.enumerated() {
                let point = CGPoint(
                    x: x(index, in: plot),
                    y: y(run.duration.seconds, in: plot, scale: scale)
                )
                if index == 0 { path.move(to: point) } else { path.addLine(to: point) }
            }
        }
        .stroke(
            Theme.Colors.chartSeries,
            style: StrokeStyle(lineWidth: Theme.Stroke.series, lineCap: .round, lineJoin: .round)
        )
    }

    @ViewBuilder
    private func referenceLine(in plot: CGSize, scale: Double) -> some View {
        if referenceSeconds > 0 {
            Path { path in
                let lineY = y(referenceSeconds, in: plot, scale: scale)
                path.move(to: CGPoint(x: 0, y: lineY))
                path.addLine(to: CGPoint(x: plot.width, y: lineY))
            }
            .stroke(
                Theme.Colors.chartReference,
                style: StrokeStyle(
                    lineWidth: Theme.Stroke.reference, dash: Theme.Stroke.referenceDash
                )
            )
        }
    }

    private func outcomeDots(in plot: CGSize, scale: Double) -> some View {
        ZStack(alignment: .topLeading) {
            ForEach(Array(runs.enumerated()), id: \.offset) { index, run in
                Circle()
                    .fill(run.outcome.isHealthy ? Theme.Colors.chartSeries : Theme.Colors.chartSeriesAlert)
                    .frame(width: Theme.Chart.dotRadius * 2, height: Theme.Chart.dotRadius * 2)
                    .position(
                        x: x(index, in: plot),
                        y: y(run.duration.seconds, in: plot, scale: scale)
                    )
                    .help(tooltip(for: run))
            }
        }
        .frame(width: plot.width, height: plot.height, alignment: .topLeading)
    }

    private var axisLabels: some View {
        VStack(alignment: .trailing) {
            Text(WGDuration(seconds: verticalScale).displayString)
            Spacer(minLength: 0)
            Text(WGDuration(seconds: verticalScale / 2).displayString)
            Spacer(minLength: 0)
            Text("0s")
        }
        .font(Theme.Typography.tabularSmall)
        .foregroundStyle(Theme.Colors.textTertiary)
        .frame(width: Theme.Chart.axisLabelWidth, height: height, alignment: .trailing)
    }

    private func tooltip(for run: Run) -> String {
        var parts: [String] = []
        if let when = run.timestamp { parts.append(Format.timestamp(when)) }
        if observable { parts.append("ran \(run.duration.displayString)") }
        parts.append("held \(run.holdDuration.displayString)")
        parts.append(run.outcome.label)
        return parts.joined(separator: " · ")
    }
}

/// A legend key.
struct LegendSwatch: View {
    let color: Color
    let label: String
    var dashed: Bool = false

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            Path { path in
                path.move(to: CGPoint(x: 0, y: Theme.IconSize.inline / 2))
                path.addLine(to: CGPoint(x: Theme.IconSize.row, y: Theme.IconSize.inline / 2))
            }
            .stroke(
                color,
                style: StrokeStyle(
                    lineWidth: Theme.Stroke.series,
                    dash: dashed ? Theme.Stroke.referenceDash : []
                )
            )
            .frame(width: Theme.IconSize.row, height: Theme.IconSize.inline)

            Text(label)
                .font(Theme.Typography.caption)
                .foregroundStyle(Theme.Colors.textSecondary)
        }
        .accessibilityElement(children: .combine)
    }
}
