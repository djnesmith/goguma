import Foundation

/// Typed access to the WakeGuard daemon.
///
/// Stateless and `Sendable`: the only stored property is a serial queue that
/// every blocking socket call is funnelled through, which both keeps I/O off
/// the main thread and off the Swift concurrency cooperative pool (a blocking
/// `read` on a pool thread starves unrelated tasks). Callers `await`; the UI
/// never blocks.
final class DaemonClient: Sendable {
    static let shared = DaemonClient()

    private let queue = DispatchQueue(label: "glass.wakeguard.ui.ipc", qos: .utility)

    init() {}

    // MARK: - Generic calls

    /// Sends `op` with a body and decodes the reply payload.
    func call<Payload: Encodable & Sendable, Result: Decodable & Sendable>(
        _ op: DaemonOp,
        payload: Payload,
        expecting: Result.Type = Result.self
    ) async throws -> Result {
        let data = try await exchange(op, payload: payload)
        return try Self.decodePayload(data, as: Result.self, op: op)
    }

    /// Sends `op` with no body and decodes the reply payload.
    func call<Result: Decodable & Sendable>(
        _ op: DaemonOp,
        expecting: Result.Type = Result.self
    ) async throws -> Result {
        let data = try await exchange(op, payload: Optional<NoPayload>.none)
        return try Self.decodePayload(data, as: Result.self, op: op)
    }

    /// Sends `op` with a body and checks only that it succeeded.
    func send(_ op: DaemonOp, payload: some Encodable & Sendable) async throws {
        let data = try await exchange(op, payload: payload)
        _ = try Self.inspectEnvelope(data)
    }

    /// Sends `op` with no body and checks only that it succeeded.
    func send(_ op: DaemonOp) async throws {
        let data = try await exchange(op, payload: Optional<NoPayload>.none)
        _ = try Self.inspectEnvelope(data)
    }

    // MARK: - Transport

    private func exchange<Payload: Encodable & Sendable>(
        _ op: DaemonOp,
        payload: Payload?
    ) async throws -> Data {
        let envelope = RequestEnvelope(op: op, payload: payload)
        let body: Data
        do {
            body = try JSONEncoder().encode(envelope)
        } catch {
            throw DaemonError.io("could not encode a \(op.rawValue) request: \(error)")
        }
        let framed = try UnixSocket.frame(body)
        let path = WGProtocol.socketPath
        let timeout = Theme.Timing.socketTimeout

        return try await withCheckedThrowingContinuation { continuation in
            queue.async {
                do {
                    let reply = try UnixSocket.roundTrip(
                        path: path, request: framed, timeout: timeout
                    )
                    continuation.resume(returning: reply)
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    // MARK: - Envelope handling

    /// First pass. Reads `protocol`, `ok`, and `error` while ignoring the body,
    /// so a payload this build cannot parse still yields the daemon's error
    /// message instead of a generic decode failure.
    @discardableResult
    private static func inspectEnvelope(_ data: Data) throws -> ResponseEnvelope<IgnoredPayload> {
        let probe: ResponseEnvelope<IgnoredPayload>
        do {
            probe = try JSONDecoder().decode(ResponseEnvelope<IgnoredPayload>.self, from: data)
        } catch {
            throw DaemonError.malformedResponse(String(describing: error))
        }
        guard probe.proto == WGProtocol.version else {
            throw DaemonError.protocolMismatch(daemon: probe.proto, app: WGProtocol.version)
        }
        guard probe.ok else {
            throw DaemonError.refused(probe.error)
        }
        return probe
    }

    /// Second pass, once the envelope is known good.
    private static func decodePayload<Result: Decodable & Sendable>(
        _ data: Data,
        as _: Result.Type,
        op: DaemonOp
    ) throws -> Result {
        try inspectEnvelope(data)
        let full: ResponseEnvelope<Result>
        do {
            full = try JSONDecoder().decode(ResponseEnvelope<Result>.self, from: data)
        } catch {
            throw DaemonError.malformedResponse(String(describing: error))
        }
        guard let payload = full.payload else {
            throw DaemonError.malformedResponse("\(op.rawValue) replied without a payload")
        }
        return payload
    }
}

// MARK: - The operations, typed

extension DaemonClient {
    func status() async throws -> DaemonStatus {
        try await call(.status, expecting: DaemonStatus.self)
    }

    func jobs() async throws -> JobsListResponse {
        try await call(.jobsList, expecting: JobsListResponse.self)
    }

    func addJob(_ job: Job) async throws {
        try await send(.jobsAdd, payload: job)
    }

    /// Upsert. Used by the edit sheet, which may be editing a job the daemon
    /// has since forgotten.
    func putJob(_ job: Job) async throws {
        try await send(.jobsPut, payload: job)
    }

    func removeJob(ref: String) async throws {
        try await send(.jobsRemove, payload: JobRefPayload(ref: ref))
    }

    func setJobEnabled(ref: String, enabled: Bool) async throws {
        try await send(.jobsEnable, payload: EnablePayload(ref: ref, enabled: enabled))
    }

    func history(ref: String, limit: Int) async throws -> HistoryResponse {
        try await call(
            .history,
            payload: HistoryPayload(ref: ref, limit: limit),
            expecting: HistoryResponse.self
        )
    }

    func config() async throws -> ConfigResponse {
        try await call(.configGet, expecting: ConfigResponse.self)
    }

    func setConfig(key: String, value: String) async throws {
        try await send(.configSet, payload: ConfigSetPayload(key: key, value: value))
    }

    /// An empty `ref` means "the next wake, whichever job it belongs to".
    func skipNext(ref: String = "") async throws {
        try await send(.skipNext, payload: JobRefPayload(ref: ref))
    }

    /// Releases every hold and lets the machine sleep.
    func sleepNow() async throws {
        try await send(.sleepNow)
    }

    /// Re-reads watched schedulers immediately, rather than waiting for the
    /// daemon's own `auto_adopt_interval`. The reply's `added` is signed:
    /// negative means jobs were retired.
    func sync() async throws -> SyncResponse {
        try await call(.sync, expecting: SyncResponse.self)
    }

    /// Holds sleep off for `duration` regardless of any job. A non-positive
    /// duration cancels an active hold.
    func keepAwake(for duration: WGDuration) async throws -> KeepAwakeResponse {
        try await call(
            .keepAwake,
            payload: KeepAwakeRequest(for: duration),
            expecting: KeepAwakeResponse.self
        )
    }

    func pause() async throws {
        try await send(.pause)
    }

    func resume() async throws {
        try await send(.resume)
    }

    /// Evaluates a pattern against the live process table, so the add sheet can
    /// say "that matches nothing" at configuration time rather than silently
    /// failing at 3am.
    func testMatch(pattern: String) async throws -> MatchTestResponse {
        try await call(
            .matchTest,
            payload: MatchTestPayload(pattern: pattern),
            expecting: MatchTestResponse.self
        )
    }
}
