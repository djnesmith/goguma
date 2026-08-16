import SwiftUI

/// Turns alarm-style choices into a cron expression.
///
/// The sheet used to ask for cron directly, then for a preset with cron behind
/// a "Custom…" escape hatch. Both put the burden in the same place: a person
/// who wants "weekdays at 8am" had to know that is `0 8 * * 1-5`, and had no
/// way to check they were right. These are the controls they already know from
/// setting an alarm (how often, at what time, on which days) and the
/// expression is derived from them and never shown.
///
/// One-way ONLY for expressions the controls cannot represent (`*/7 3-5 * * 2`
/// has no alarm equivalent); a job carrying one keeps it untouched unless the
/// user changes something here. Everything these controls can produce is
/// parsed back by `parse` so the edit sheet opens showing the job's real
/// schedule: fabricating defaults instead meant touching any control silently
/// rewrote the fire time from those defaults.
enum ScheduleBuilder {
    enum Repeat: String, CaseIterable, Hashable {
        case daily
        case weekly
        case monthly
        case everyNHours
        case everyNMinutes

        var label: String {
            switch self {
            case .daily: "Every day"
            case .weekly: "On certain days"
            case .monthly: "Once a month"
            case .everyNHours: "Every few hours"
            case .everyNMinutes: "Every few minutes"
            }
        }

        /// Whether a time of day is meaningful. A job repeating inside a day
        /// has a minute but no hour to choose, so offering a full clock would
        /// imply otherwise.
        var usesTime: Bool {
            switch self {
            case .daily, .weekly, .monthly: true
            case .everyNHours, .everyNMinutes: false
            }
        }
    }

    /// The hour counts offered by "Every few hours".
    ///
    /// Every divisor of 24 plus 1, so the fires land at the same times every
    /// day. A step that does not divide 24 (5, say) means cron restarts the
    /// cycle at midnight and the last gap of the day is short, which is a
    /// schedule nobody asks for and cannot see they have chosen.
    static let hourChoices = [1, 2, 3, 4, 6, 8, 12]

    /// The minute counts offered by "Every few minutes", on the same rule.
    static let minuteChoices = [1, 2, 5, 10, 15, 20, 30]

    /// The cron five-field expression for a set of choices.
    ///
    /// Weekday numbers are cron's own: Sunday is 0. An empty selection would
    /// produce a job that never fires, so it falls back to every day, a
    /// schedule that runs too often is recoverable, one that never runs is the
    /// failure this product exists to prevent.
    static func expression(
        repeating: Repeat, at time: Date, weekdays: Set<Int>,
        everyHours: Int, everyMinutes: Int, dayOfMonth: Int
    ) -> String {
        let parts = Calendar.current.dateComponents([.hour, .minute], from: time)
        let hour = parts.hour ?? 9
        let minute = parts.minute ?? 0

        switch repeating {
        case .daily:
            return "\(minute) \(hour) * * *"
        case .weekly:
            let days = weekdays.isEmpty
                ? "*"
                : weekdays.sorted().map(String.init).joined(separator: ",")
            return "\(minute) \(hour) * * \(days)"
        case .monthly:
            return "\(minute) \(hour) \(min(max(dayOfMonth, 1), 28)) * *"
        case .everyNHours:
            let n = max(1, everyHours)
            return n == 1 ? "\(minute) * * * *" : "\(minute) */\(n) * * *"
        case .everyNMinutes:
            let n = max(1, everyMinutes)
            return n == 1 ? "* * * * *" : "*/\(n) * * * *"
        }
    }
}

/// The seven-day row from an alarm.
struct WeekdayPicker: View {
    @Binding var selected: Set<Int>

    /// Cron's numbering, starting on Sunday, so the labels and the expression
    /// cannot drift apart.
    private static let days: [(number: Int, label: String)] = [
        (0, "S"), (1, "M"), (2, "T"), (3, "W"), (4, "T"), (5, "F"), (6, "S"),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.xs) {
                ForEach(Self.days, id: \.number) { day in
                    dayButton(day.number, day.label)
                }
                Spacer(minLength: 0)
            }

            // What is actually selected, in words.
            //
            // Seven single letters with two pairs that repeat ("T" and "S")
            // cannot be read back reliably even when the fill is obvious, and
            // "which days is this on again" is the only question this control
            // exists to answer. Saying it out loud costs one caption line.
            Text(summary)
                .font(Theme.Typography.caption)
                .foregroundStyle(
                    selected.isEmpty ? Theme.Colors.warning : Theme.Colors.textSecondary
                )
        }
    }

    private func dayButton(_ number: Int, _ label: String) -> some View {
        let isOn = selected.contains(number)
        return Button {
            if isOn { selected.remove(number) } else { selected.insert(number) }
        } label: {
            Text(label)
                // Selected days are heavier as well as filled, so the state
                // survives being looked at quickly and does not rely on colour
                // alone.
                .font(isOn ? Theme.Typography.rowLabel : Theme.Typography.body)
                .frame(width: 30, height: 26)
                .background(
                    isOn ? Theme.Colors.accent : Color.clear,
                    in: RoundedRectangle(cornerRadius: Theme.Radius.badge, style: .continuous)
                )
                // An unselected day is an empty outlined box rather than a
                // grey filled one. Filled-vs-filled read as one row of
                // buttons in two slightly different greys, which is not a
                // state anyone can see.
                .overlay(
                    RoundedRectangle(cornerRadius: Theme.Radius.badge, style: .continuous)
                        .strokeBorder(
                            isOn ? Color.clear : Theme.Colors.divider,
                            lineWidth: Theme.Stroke.hairline
                        )
                )
                .foregroundStyle(isOn ? Color.white : Theme.Colors.textSecondary)
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(Self.accessibleName(number))
        .accessibilityAddTraits(isOn ? .isSelected : [])
    }

    /// "Weekdays", "Every day", "Mon, Wed, Fri", or a warning when nothing is
    /// chosen, because an empty selection silently means every day.
    private var summary: String {
        if selected.isEmpty { return "No days selected, so it will run every day" }
        if selected == [1, 2, 3, 4, 5] { return "Weekdays" }
        if selected == [0, 6] { return "Weekends" }
        if selected.count == 7 { return "Every day" }
        let short = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
        return selected.sorted().map { short[$0] }.joined(separator: ", ")
    }

    /// Two days share the letter "T" and two share "S", so the button label is
    /// not enough for anyone who cannot see the row.
    private static func accessibleName(_ number: Int) -> String {
        ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"][number]
    }
}

/// Hours, minutes and AM/PM as three menus.
///
/// `DatePicker(displayedComponents: .hourAndMinute)` is a stepper over a text
/// field: it clipped "AM" at the field's edge, and its arrows moved the whole
/// field by an hour, so setting 9:15 meant typing into it and hoping. Three
/// menus set any minute of any hour in three clicks and cannot be mistyped.
struct ClockPicker: View {
    @Binding var date: Date

    private var calendar: Calendar { Calendar.current }

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            Picker("", selection: hour) {
                ForEach(1...12, id: \.self) { Text("\($0)").tag($0) }
            }
            .labelsHidden()
            .frame(width: 62)

            Text(":")
                .font(Theme.Typography.rowLabel)
                .foregroundStyle(Theme.Colors.textSecondary)

            Picker("", selection: minute) {
                ForEach(0...59, id: \.self) { Text(String(format: "%02d", $0)).tag($0) }
            }
            .labelsHidden()
            .frame(width: 66)

            Picker("", selection: isPM) {
                Text("AM").tag(false)
                Text("PM").tag(true)
            }
            .labelsHidden()
            .frame(width: 70)

            Spacer(minLength: 0)
        }
    }

    private var parts: DateComponents {
        calendar.dateComponents([.hour, .minute], from: date)
    }

    private func set(hour24: Int, minute: Int) {
        if let d = calendar.date(bySettingHour: hour24, minute: minute, second: 0, of: date) {
            date = d
        }
    }

    /// 12-hour clock, so midnight reads as 12 rather than 0.
    private var hour: Binding<Int> {
        Binding(
            get: {
                let h = parts.hour ?? 9
                let twelve = h % 12
                return twelve == 0 ? 12 : twelve
            },
            set: { new in
                let pm = (parts.hour ?? 9) >= 12
                let base = new % 12
                set(hour24: pm ? base + 12 : base, minute: parts.minute ?? 0)
            }
        )
    }

    private var minute: Binding<Int> {
        Binding(
            get: { parts.minute ?? 0 },
            set: { set(hour24: parts.hour ?? 9, minute: $0) }
        )
    }

    private var isPM: Binding<Bool> {
        Binding(
            get: { (parts.hour ?? 9) >= 12 },
            set: { pm in
                let h = parts.hour ?? 9
                let base = h % 12
                set(hour24: pm ? base + 12 : base, minute: parts.minute ?? 0)
            }
        )
    }
}

extension ScheduleBuilder {
    struct Choices {
        var repeating: Repeat
        var time: Date
        var weekdays: Set<Int>
        var everyHours: Int
        var everyMinutes: Int
        var dayOfMonth: Int
    }

    /// Defaults for the controls a given expression says nothing about, so a
    /// job that repeats hourly still opens with a sensible day-of-month rather
    /// than a zero.
    static func defaults(
        _ repeating: Repeat, time: Date,
        weekdays: Set<Int> = [1, 2, 3, 4, 5],
        everyHours: Int = 6, everyMinutes: Int = 15, dayOfMonth: Int = 1
    ) -> Choices {
        Choices(
            repeating: repeating, time: time, weekdays: weekdays,
            everyHours: everyHours, everyMinutes: everyMinutes, dayOfMonth: dayOfMonth
        )
    }

    /// The inverse of `expression`, for the shapes those controls produce
    /// (plus plain weekday ranges, which imported crontabs commonly carry).
    /// Returns nil for anything else, in which case the sheet keeps the
    /// expression untouched.
    static func parse(_ cron: String) -> Choices? {
        let fields = cron.split(separator: " ").map(String.init)
        guard fields.count == 5, fields[3] == "*" else { return nil }

        func clock(_ hour: Int, _ minute: Int) -> Date {
            Calendar.current.date(bySettingHour: hour, minute: minute, second: 0, of: Date())
                ?? Date()
        }

        let minuteField = fields[0], hourField = fields[1]
        let dom = fields[2], dow = fields[4]

        // Sub-hour repeats: the minute field carries the step.
        if dom == "*", dow == "*", hourField == "*" {
            if minuteField == "*" {
                return defaults(.everyNMinutes, time: clock(9, 0), everyMinutes: 1)
            }
            if minuteField.hasPrefix("*/"), let n = Int(minuteField.dropFirst(2)),
               (1...59).contains(n)
            {
                return defaults(.everyNMinutes, time: clock(9, 0), everyMinutes: n)
            }
        }

        guard let minute = Int(minuteField), (0...59).contains(minute) else { return nil }

        // Hourly is "every 1 hour", so it and every-N-hours are one control.
        if hourField == "*", dom == "*", dow == "*" {
            return defaults(.everyNHours, time: clock(9, minute), everyHours: 1)
        }
        if hourField.hasPrefix("*/"), dom == "*", dow == "*",
           let n = Int(hourField.dropFirst(2)), (1...23).contains(n)
        {
            return defaults(.everyNHours, time: clock(9, minute), everyHours: n)
        }

        guard let hour = Int(hourField), (0...23).contains(hour) else { return nil }
        if dow == "*", let day = Int(dom), (1...28).contains(day) {
            return defaults(.monthly, time: clock(hour, minute), dayOfMonth: day)
        }
        guard dom == "*" else { return nil }
        if dow == "*" {
            return defaults(.daily, time: clock(hour, minute))
        }
        guard let days = weekdaySet(dow), !days.isEmpty else { return nil }
        return defaults(.weekly, time: clock(hour, minute), weekdays: days)
    }

    /// Parses a cron day-of-week field of plain numbers, lists and ranges.
    /// 7 is Sunday, same as 0. Steps and names are left to the escape hatch.
    private static func weekdaySet(_ field: String) -> Set<Int>? {
        var out: Set<Int> = []
        for part in field.split(separator: ",") {
            if let dash = part.firstIndex(of: "-") {
                guard let lo = Int(part[..<dash]), let hi = Int(part[part.index(after: dash)...]),
                      (0...7).contains(lo), (0...7).contains(hi), lo <= hi
                else { return nil }
                for d in lo...hi { out.insert(d % 7) }
                continue
            }
            guard let d = Int(part), (0...7).contains(d) else { return nil }
            out.insert(d % 7)
        }
        return out
    }
}
