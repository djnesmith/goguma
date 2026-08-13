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
        case hourly
        case everyNHours
        case monthly

        var label: String {
            switch self {
            case .daily: "Every day"
            case .weekly: "On certain days"
            case .hourly: "Every hour"
            case .everyNHours: "Every few hours"
            case .monthly: "Monthly, on the 1st"
            }
        }

        /// Whether a time of day is meaningful. An hourly job has a minute but
        /// no hour to choose, so offering a full clock would imply otherwise.
        var usesTime: Bool {
            switch self {
            case .daily, .weekly, .monthly: true
            case .hourly, .everyNHours: false
            }
        }
    }

    /// The cron five-field expression for a set of choices.
    ///
    /// Weekday numbers are cron's own: Sunday is 0. An empty selection would
    /// produce a job that never fires, so it falls back to every day, a
    /// schedule that runs too often is recoverable, one that never runs is the
    /// failure this product exists to prevent.
    static func expression(
        repeating: Repeat, at time: Date, weekdays: Set<Int>, everyHours: Int
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
        case .hourly:
            return "\(minute) * * * *"
        case .everyNHours:
            return "\(minute) */\(max(1, everyHours)) * * *"
        case .monthly:
            return "\(minute) \(hour) 1 * *"
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
        HStack(spacing: Theme.Space.xs) {
            ForEach(Self.days, id: \.number) { day in
                let isOn = selected.contains(day.number)
                Button {
                    if isOn { selected.remove(day.number) } else { selected.insert(day.number) }
                } label: {
                    Text(day.label)
                        .font(Theme.Typography.rowLabel)
                        .frame(width: 30, height: 26)
                        .background(
                            isOn ? Theme.Colors.accent : Theme.Colors.divider.opacity(0.5),
                            in: RoundedRectangle(cornerRadius: Theme.Radius.badge)
                        )
                        .foregroundStyle(isOn ? Theme.Colors.card : Theme.Colors.textSecondary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(Self.accessibleName(day.number))
                .accessibilityAddTraits(isOn ? .isSelected : [])
            }
            Spacer(minLength: 0)
        }
    }

    /// Two days share the letter "T" and two share "S", so the button label is
    /// not enough for anyone who cannot see the row.
    private static func accessibleName(_ number: Int) -> String {
        ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"][number]
    }
}

extension ScheduleBuilder {
    struct Choices {
        var repeating: Repeat
        var time: Date
        var weekdays: Set<Int>
        var everyHours: Int
    }

    /// The inverse of `expression`, for the shapes those controls produce
    /// (plus plain weekday ranges, which imported crontabs commonly carry).
    /// Returns nil for anything else, in which case the sheet keeps the
    /// expression untouched.
    static func parse(_ cron: String) -> Choices? {
        let fields = cron.split(separator: " ").map(String.init)
        guard fields.count == 5, fields[3] == "*" else { return nil }
        guard let minute = Int(fields[0]), (0...59).contains(minute) else { return nil }

        func clock(_ hour: Int) -> Date {
            Calendar.current.date(bySettingHour: hour, minute: minute, second: 0, of: Date())
                ?? Date()
        }

        let hourField = fields[1], dom = fields[2], dow = fields[4]

        if hourField == "*", dom == "*", dow == "*" {
            return Choices(repeating: .hourly, time: clock(9), weekdays: [1, 2, 3, 4, 5], everyHours: 6)
        }
        if hourField.hasPrefix("*/"), dom == "*", dow == "*",
           let n = Int(hourField.dropFirst(2)), (1...23).contains(n)
        {
            return Choices(repeating: .everyNHours, time: clock(9), weekdays: [1, 2, 3, 4, 5], everyHours: n)
        }
        guard let hour = Int(hourField), (0...23).contains(hour) else { return nil }
        if dom == "1", dow == "*" {
            return Choices(repeating: .monthly, time: clock(hour), weekdays: [1, 2, 3, 4, 5], everyHours: 6)
        }
        guard dom == "*" else { return nil }
        if dow == "*" {
            return Choices(repeating: .daily, time: clock(hour), weekdays: [1, 2, 3, 4, 5], everyHours: 6)
        }
        guard let days = weekdaySet(dow), !days.isEmpty else { return nil }
        return Choices(repeating: .weekly, time: clock(hour), weekdays: days, everyHours: 6)
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
