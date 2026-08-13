import Foundation

/// A duration as the daemon speaks it.
///
/// Go's `model.Duration` marshals to a human string (`"90s"`, `"5m"`,
/// `"1h 30m"`, `""` for zero) because config files and `--json` output are both
/// read by people. But it *unmarshals* from either a string or a bare integer
/// of nanoseconds, and some fields round-trip a raw `time.Duration`, so this
/// side accepts both. Anything unparseable reads as zero rather than failing
/// the enclosing object: a garbled `ceiling` must not blank a job row.
struct WGDuration: Codable, Sendable, Hashable, Comparable {
    /// Canonical storage. Seconds rather than nanoseconds so chart maths stays
    /// in a range `Double` represents exactly.
    var seconds: Double

    init(seconds: Double) { self.seconds = seconds }

    static let zero = WGDuration(seconds: 0)

    var isZero: Bool { seconds == 0 }
    var timeInterval: TimeInterval { seconds }

    static func < (lhs: WGDuration, rhs: WGDuration) -> Bool { lhs.seconds < rhs.seconds }

    // MARK: - Codable

    init(from decoder: any Decoder) throws {
        guard let container = try? decoder.singleValueContainer() else {
            self.seconds = 0
            return
        }
        if container.decodeNil() {
            self.seconds = 0
            return
        }
        if let text = try? container.decode(String.self) {
            self.seconds = WGDuration.parse(text) ?? 0
            return
        }
        // A bare number is nanoseconds, matching Go's `time.Duration`.
        if let nanos = try? container.decode(Double.self) {
            self.seconds = nanos / 1_000_000_000
            return
        }
        self.seconds = 0
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(wireString)
    }

    // MARK: - Parsing

    /// Parses the daemon's duration grammar into seconds.
    ///
    /// Accepts multi-unit strings with or without separating spaces
    /// (`"1h 30m"`, `"1h30m0s"`), fractional values (`"3.4s"`), the sub-second
    /// units Go emits (`"150ms"`), and Go's day/week extensions (`"2d"`,
    /// `"1w"`). A bare number with no unit is read as seconds. Returns nil when
    /// nothing sensible can be made of the input.
    static func parse(_ raw: String) -> Double? {
        var text = raw.lowercased().filter { !$0.isWhitespace }
        if text.isEmpty { return nil }

        var sign = 1.0
        if text.hasPrefix("-") {
            sign = -1
            text.removeFirst()
        } else if text.hasPrefix("+") {
            text.removeFirst()
        }
        if text.isEmpty { return nil }

        var total = 0.0
        var sawComponent = false
        var index = text.startIndex

        while index < text.endIndex {
            // Number part.
            let numberStart = index
            while index < text.endIndex, text[index].isNumber || text[index] == "." {
                index = text.index(after: index)
            }
            guard numberStart < index, let magnitude = Double(text[numberStart..<index]) else {
                return sawComponent ? sign * total : nil
            }

            // Unit part.
            let unitStart = index
            while index < text.endIndex, text[index].isLetter {
                index = text.index(after: index)
            }
            let unit = String(text[unitStart..<index])

            guard let multiplier = unitSeconds[unit] else {
                // Unknown unit: keep what we understood rather than discarding
                // the whole value.
                return sawComponent ? sign * total : nil
            }
            total += magnitude * multiplier
            sawComponent = true
        }

        return sawComponent ? sign * total : nil
    }

    /// Unit table. An empty unit means a bare number, read as seconds.
    private static let unitSeconds: [String: Double] = [
        "": 1,
        "ns": 1e-9,
        "us": 1e-6,
        "\u{00B5}s": 1e-6, // MICRO SIGN
        "\u{03BC}s": 1e-6, // GREEK SMALL LETTER MU
        "ms": 1e-3,
        "s": 1,
        "sec": 1,
        "m": 60,
        "min": 60,
        "h": 3600,
        "hr": 3600,
        "d": 86_400,
        "w": 604_800,
    ]

    // MARK: - Rendering

    /// Compact form for the wire, mirroring what Go's `time.ParseDuration`
    /// accepts. Deliberately *not* the display form: Go rejects the spaces in
    /// `"1h 30m"`, and rejects `d`/`w` in a multi-unit string, so this emits
    /// only `h`/`m`/`s`/`ms` run together. Zero encodes as the empty string,
    /// which is how Go signals "unset, use the default".
    var wireString: String {
        if seconds == 0 { return "" }
        if seconds < 0 { return "-" + WGDuration(seconds: -seconds).wireString }

        if seconds < 1 {
            return "\(Int((seconds * 1000).rounded()))ms"
        }

        var remaining = Int(seconds.rounded())
        let hours = remaining / 3600
        remaining %= 3600
        let minutes = remaining / 60
        let secs = remaining % 60

        var out = ""
        if hours > 0 { out += "\(hours)h" }
        if minutes > 0 { out += "\(minutes)m" }
        if secs > 0 || out.isEmpty { out += "\(secs)s" }
        return out
    }

    /// Human form, matching the CLI's `HumanDuration`: compact, at most two
    /// units. Sub-minute values keep a decimal because job runtimes are often a
    /// few seconds and "3s" vs "3.4s" matters when tuning a ceiling.
    /// How long until something happens, for a value that is on screen while
    /// it counts down.
    ///
    /// Whole minutes until the last one, then seconds: `54m`, `3m`, `1m`,
    /// `59s`, `8s`. `displayString` gives `54m 34s`, which is right for a
    /// duration you are reading once (a ceiling, a schedule) and wrong for one
    /// that is ticking: the seconds column changes every second for an hour,
    /// which draws the eye continuously to a number nobody is watching until
    /// the end. Dropping it means the value is still while it does not matter
    /// and lively in the minute when it does.
    var countdownString: String {
        if seconds < 0 { return "-" + WGDuration(seconds: -seconds).countdownString }
        // Unit choice follows the ROUNDED value. Branching on the raw value
        // showed "in 60s" and "in 60m" for a full second at each boundary,
        // on exactly the counters users watch tick down.
        let total = Int(seconds.rounded())
        if total < 60 { return "\(total)s" }
        if total < 3600 { return "\(total / 60)m" }
        if total < 86_400 {
            let h = total / 3600
            let m = (total % 3600) / 60
            return m == 0 ? "\(h)h" : "\(h)h \(m)m"
        }
        // Beyond a day, `displayString` already reads coarsely enough.
        return displayString
    }

    var displayString: String {
        if seconds < 0 { return "-" + WGDuration(seconds: -seconds).displayString }
        if seconds == 0 { return "0s" }

        if seconds < 1 {
            return "\(Int((seconds * 1000).rounded()))ms"
        }
        let tenths = (seconds * 10).rounded() / 10
        if tenths < 60 {
            if tenths == tenths.rounded() { return "\(Int(tenths))s" }
            return String(format: "%.1fs", tenths)
        }
        // Same rounded-value rule as countdownString: raw-value branches
        // rendered "60s", "60m" and "24h" for a second at each boundary.
        let total = Int(seconds.rounded())
        if total < 3600 {
            let m = total / 60
            let s = total % 60
            return s == 0 ? "\(m)m" : "\(m)m \(s)s"
        }
        if total < 86_400 {
            let h = total / 3600
            let m = (total % 3600) / 60
            return m == 0 ? "\(h)h" : "\(h)h \(m)m"
        }
        let d = total / 86_400
        let h = (total % 86_400) / 3600
        return h == 0 ? "\(d)d" : "\(d)d \(h)h"
    }

    /// `mm:ss` / `h:mm:ss`, for the live elapsed counter that ticks once a
    /// second. Monospaced digits at the call site keep it from jittering.
    var clockString: String {
        let total = Int(max(0, seconds))
        let h = total / 3600
        let m = (total % 3600) / 60
        let s = total % 60
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, s) }
        return String(format: "%02d:%02d", m, s)
    }
}

extension WGDuration {
    /// `nil` when zero, so views can render "-" for "not set" without every
    /// call site repeating the comparison.
    var displayOrNil: String? { isZero ? nil : displayString }
}
