import Foundation

/// Wire constants and envelopes shared by every request.
enum WGProtocol {
    /// Bumped by the daemon on any breaking request/response shape change. The
    /// menu bar app ships independently of the daemon, so both sides check this
    /// and say so plainly rather than misrendering each other's data.
    static let version = 1

    /// Upper bound on a single frame, matching the daemon's `maxFrame`. Exists
    /// so a corrupt or hostile length prefix cannot make us allocate unbounded
    /// memory.
    static let maxFrameBytes = 8 << 20

    /// Default state directory. `GOGUMA_STATE_DIR` overrides it, exactly as
    /// on the Go side, so a dev instance can be pointed at its own tree.
    static var socketPath: String {
        if let override = ProcessInfo.processInfo.environment["GOGUMA_STATE_DIR"],
           !override.isEmpty
        {
            return (override as NSString).expandingTildeInPath + "/daemon.sock"
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home
            .appending(path: "Library/Application Support/goguma/daemon.sock")
            .path(percentEncoded: false)
    }
}

/// Every request the app can make. Namespaced by subject, matching `ipc.Op`.
enum DaemonOp: String, Sendable {
    case ping
    case status
    case jobsList = "jobs.list"
    case jobsAdd = "jobs.add"
    case jobsPut = "jobs.put"
    case jobsRemove = "jobs.remove"
    case jobsEnable = "jobs.enable"
    case history
    case configGet = "config.get"
    case configSet = "config.set"
    case skipNext = "skip_next"
    case sleepNow = "sleep_now"
    case keepAwake = "keep_awake"
    case pause
    case resume
    /// Re-read every watched scheduler now and adopt or retire accordingly.
    case sync
    case matchTest = "match.test"
}

/// Request envelope: `{"protocol":1,"op":"status","payload":{…}}`.
struct RequestEnvelope<Payload: Encodable & Sendable>: Encodable, Sendable {
    var proto: Int = WGProtocol.version
    var op: DaemonOp
    var payload: Payload?

    enum CodingKeys: String, CodingKey {
        case proto = "protocol"
        case op
        case payload
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(proto, forKey: .proto)
        try c.encode(op.rawValue, forKey: .op)
        try c.encodeIfPresent(payload, forKey: .payload)
    }
}

/// An empty request body, for ops that take no payload.
struct NoPayload: Encodable, Sendable {}

/// Response envelope: `{"protocol":1,"ok":true,"error":"","payload":{…}}`.
///
/// Generic over the payload so the whole reply decodes in one pass. It is
/// decoded twice in practice: once with `IgnoredPayload` to read `protocol`,
/// `ok`, and `error` (which must survive a payload this build cannot parse)
/// and then again for the body.
struct ResponseEnvelope<Payload: Decodable & Sendable>: Decodable, Sendable {
    var proto: Int
    var ok: Bool
    var error: String
    var payload: Payload?

    enum CodingKeys: String, CodingKey {
        case proto = "protocol"
        case ok, error, payload
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // A missing `protocol` is treated as the version we speak: refusing to
        // render because a field was omitted would be worse than trying.
        proto = c.value(.proto, WGProtocol.version)
        ok = c.value(.ok, false)
        error = c.value(.error, "")
        payload = try? c.decodeIfPresent(Payload.self, forKey: .payload)
    }
}

// MARK: - Errors

/// Everything that can go wrong between this app and the daemon, phrased so the
/// UI can show it verbatim.
enum DaemonError: Error, LocalizedError, Sendable, Equatable {
    /// No socket file at all; goguma has probably never been installed.
    case notInstalled(path: String)
    /// Socket file exists but nothing is listening; the daemon is stopped or
    /// crashed, leaving a stale node behind.
    case notRunning(path: String)
    /// Socket path exists but we may not open it.
    case permissionDenied(path: String)
    /// Read or write failed mid-conversation.
    case io(String)
    /// The daemon accepted the request but did not answer within the deadline.
    case timedOut
    /// Version skew between this app and the daemon.
    case protocolMismatch(daemon: Int, app: Int)
    /// The daemon answered `ok: false`.
    case refused(String)
    /// The reply did not decode.
    case malformedResponse(String)

    var errorDescription: String? {
        switch self {
        case .notInstalled:
            "goguma isn't running."
        case .notRunning:
            "goguma isn't running."
        case let .permissionDenied(path):
            "Can't open the goguma socket at \(path)."
        case let .io(detail):
            "Lost contact with goguma's background service: \(detail)"
        case .timedOut:
            "goguma's background service didn't respond."
        case let .protocolMismatch(daemon, app):
            "goguma's background service and this app are different versions, "
                + "update both. The service speaks protocol \(daemon); "
                + "this app speaks \(app)."
        case let .refused(message):
            message.isEmpty ? "goguma's background service refused the request." : message
        case let .malformedResponse(detail):
            "Unreadable reply from goguma's background service: \(detail)"
        }
    }

    /// What the user should do next. Empty when there's nothing useful to say.
    var recoveryHint: String {
        switch self {
        // Both of these used to name a command the reader may not have. Someone
        // who installed the CLI does have `goguma` on their PATH; someone who
        // downloaded the app has it only inside the bundle, and telling them to
        // run it in Terminal is advice that cannot be followed. Onboarding knows
        // which case this is.
        // Never "open the goguma menu and choose Set up". This text is read
        // inside that menu, next to that button, so it described the reader's
        // current situation as the thing they should go and do.
        case .notInstalled:
            Onboarding.canSelfInstall
                ? "Takes a few seconds. macOS will ask for your password."
                : "Run `goguma install` in Terminal to set it up."
        case .notRunning:
            Onboarding.canSelfInstall
                ? "Takes a few seconds. macOS will ask for your password, which is "
                    + "what lets goguma wake a sleeping Mac."
                : "Run `goguma install` in Terminal to set it up. It asks for your Mac "
                    + "login password, which macOS needs to install the part that can wake "
                    + "a sleeping Mac."
        case .permissionDenied:
            "Check the permissions on ~/Library/Application Support/goguma."
        case .timedOut:
            "It may be busy. This will retry on its own."
        case .protocolMismatch:
            "Update goguma's background service and this app to matching versions."
        case .io, .refused, .malformedResponse:
            ""
        }
    }

    /// True when the daemon simply isn't there, as opposed to being there and
    /// unhappy. Drives the "daemon not running" empty state in every window.
    var isUnreachable: Bool {
        switch self {
        case .notInstalled, .notRunning, .permissionDenied, .timedOut, .io: true
        case .protocolMismatch, .refused, .malformedResponse: false
        }
    }
}
