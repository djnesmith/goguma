import Foundation

// MARK: - Defensive decoding helpers
//
// The daemon ships independently of this app. A field may be added, may be
// null, may be omitted entirely, or may carry an enum case this build has never
// heard of. None of those may throw — a missing `wake_error` must not blank the
// whole status view. Every accessor below swallows the error and substitutes a
// default, so decoding degrades field by field instead of all at once.

extension KeyedDecodingContainer {
    /// Decode `key`, falling back to `fallback` when absent, null, or malformed.
    func value<T: Decodable>(_ key: Key, _ fallback: T) -> T {
        guard let decoded = try? decodeIfPresent(T.self, forKey: key) else { return fallback }
        return decoded
    }

    /// Decode `key` as an optional; absent, null, and malformed all yield nil.
    func optional<T: Decodable>(_ key: Key, as _: T.Type = T.self) -> T? {
        guard let decoded = try? decodeIfPresent(T.self, forKey: key) else { return nil }
        return decoded
    }
}

/// A payload placeholder for responses whose body we ignore, and for the first
/// envelope-inspection pass. Decodes successfully against literally any JSON,
/// including `null` and absence.
struct IgnoredPayload: Decodable, Sendable {
    init() {}
    init(from _: any Decoder) throws {}
}

// MARK: - Lenient enums
//
// An unrecognised string is a legitimate runtime condition (newer daemon), not
// a decode failure, so every wire enum carries an `unknown` case and its raw
// text for display.

/// A string enum that never fails to decode.
protocol LenientRawEnum: RawRepresentable, Codable, Sendable, Hashable where RawValue == String {
    static var unknownCase: Self { get }
}

extension LenientRawEnum {
    init(from decoder: any Decoder) throws {
        let raw = (try? decoder.singleValueContainer().decode(String.self)) ?? ""
        self = Self(rawValue: raw) ?? Self.unknownCase
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(rawValue)
    }
}

// MARK: - Timestamps
//
// Go emits RFC 3339 with an offset ("2026-08-05T08:58:30+08:00"), sometimes
// with fractional seconds, sometimes as UTC "Z". Fields marked `omitzero` are
// dropped when zero, but older or hand-written payloads may still carry the Go
// zero time ("0001-01-01T00:00:00Z"), which must read as "no value" rather than
// as a date two millennia ago.

/// A `Date` that decodes leniently from the daemon's timestamp formats.
struct WGDate: Codable, Sendable, Hashable, Comparable {
    var date: Date

    init(_ date: Date) { self.date = date }

    init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = try container.decode(String.self)
        guard let parsed = Self.parse(raw) else {
            throw DecodingError.dataCorruptedError(
                in: container, debugDescription: "unrecognised timestamp \(raw)"
            )
        }
        self.date = parsed
    }

    func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(Self.style.format(date))
    }

    static func < (lhs: WGDate, rhs: WGDate) -> Bool { lhs.date < rhs.date }

    /// True for Go's zero time, which several fields use to mean "never".
    var isZeroTime: Bool { date < Self.zeroTimeThreshold }

    /// nil when this is Go's zero time, otherwise the date.
    var nonZero: Date? { isZeroTime ? nil : date }

    // Anything before 1971 is not a real observation of a cron job; Go's zero
    // time (year 1) is the only value that lands there in practice.
    private static let zeroTimeThreshold = Date(timeIntervalSince1970: 31_536_000)

    /// `Date.ISO8601FormatStyle` rather than `ISO8601DateFormatter`: it is a
    /// `Sendable` value type (so it can be a shared `static let` under strict
    /// concurrency) and it already accepts every shape Go emits here —
    /// fractional seconds present or absent, `Z` or a numeric offset.
    private static let style = Date.ISO8601FormatStyle()

    static func parse(_ raw: String) -> Date? {
        let trimmed = raw.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return nil }
        return try? style.parse(trimmed)
    }
}

extension Optional where Wrapped == WGDate {
    /// Collapses both "absent" and "Go zero time" into nil, which is what every
    /// call site actually wants.
    var meaningful: Date? { self?.nonZero }
}
