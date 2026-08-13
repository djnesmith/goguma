import Foundation

/// Date and number rendering shared by every surface.
///
/// Uses `FormatStyle` throughout rather than `DateFormatter`: the styles are
/// value types, so they are `Sendable` and safe to use from anywhere without a
/// cached-formatter singleton.
enum Format {

    /// Wall-clock time only, e.g. "08:58", or "8:58 AM" where the user's
    /// locale prefers it. Used for the menu bar title, so it must stay short.
    static func clock(_ date: Date) -> String {
        date.formatted(.dateTime.hour().minute())
    }

    /// Day plus time, e.g. "Aug 5, 08:58". Used where a run could be from any
    /// day and a bare time would be ambiguous.
    static func dayAndTime(_ date: Date) -> String {
        date.formatted(.dateTime.month(.abbreviated).day().hour().minute())
    }

    /// Full timestamp for a history row.
    static func timestamp(_ date: Date) -> String {
        date.formatted(.dateTime.year().month(.abbreviated).day().hour().minute().second())
    }

    /// "in 13h 59m" / "overdue by 2m", matching the CLI's `HumanUntil`.
    static func until(_ target: Date, from now: Date) -> String {
        let delta = target.timeIntervalSince(now)
        if delta < 0 {
            return "overdue by " + WGDuration(seconds: -delta).countdownString
        }
        return "in " + WGDuration(seconds: delta).countdownString
    }

    /// Bare relative magnitude, without the "in "/"overdue by " prefix.
    static func gap(_ target: Date, from now: Date) -> String {
        WGDuration(seconds: abs(target.timeIntervalSince(now))).displayString
    }

    /// "3 minutes ago" style, for "last updated".
    static func ago(_ date: Date, from now: Date) -> String {
        let delta = now.timeIntervalSince(date)
        if delta < 2 { return "just now" }
        return WGDuration(seconds: delta).displayString + " ago"
    }

    /// Temperature with one decimal, e.g. "38.6°C". Nil input yields nil so the
    /// caller can omit the whole row rather than print "-°C".
    static func temperature(_ celsius: Double?) -> String? {
        guard let celsius else { return nil }
        return String(format: "%.1f°C", celsius)
    }

    static func percent(_ value: Int) -> String { "\(value)%" }

    /// A count with its noun pluralised. `count(1, "run")` → "1 run".
    static func count(_ n: Int, _ noun: String, plural: String? = nil) -> String {
        n == 1 ? "1 \(noun)" : "\(n) \(plural ?? noun + "s")"
    }

    /// Binds the last two words of a sentence together so a paragraph never
    /// ends with a single word stranded on its own line.
    ///
    /// A widow ("…releasing\nshortly.") reads as a layout bug even when the
    /// text is correct, and this app puts a lot of variable-length prose into
    /// narrow columns, so it happens constantly. A non-breaking space is the
    /// typographic fix: SwiftUI's line breaker will not split on U+00A0, so the
    /// pair moves down together and the ragged edge absorbs it instead.
    ///
    /// Only the final gap is bound. Doing more would start forcing overflow in
    /// a 340pt popover, which is a worse defect than the one being fixed.
    static func noWidow(_ text: String) -> String {
        guard let lastSpace = text.lastIndex(of: " ") else { return text }
        // A long final word plus a long penultimate word can exceed the column
        // on its own; binding those two would push the line into truncation.
        let tail = text[text.index(after: lastSpace)...]
        let head = text[..<lastSpace]
        guard let penultimate = head.lastIndex(of: " ") else {
            return tail.count > 14 ? text : text.replacingLastSpaceWithNonBreaking()
        }
        let penult = head[head.index(after: penultimate)...]
        guard penult.count + tail.count <= 18 else { return text }
        return text.replacingLastSpaceWithNonBreaking()
    }

    /// The placeholder for a value the daemon didn't give us.
    static let empty = "-"

    /// Renders an optional string, substituting the em-dash placeholder.
    static func orEmpty(_ text: String?) -> String {
        guard let text, !text.isEmpty else { return empty }
        return text
    }
}

private extension String {
    func replacingLastSpaceWithNonBreaking() -> String {
        guard let last = lastIndex(of: " ") else { return self }
        return replacingCharacters(in: last...last, with: "\u{00A0}")
    }
}
