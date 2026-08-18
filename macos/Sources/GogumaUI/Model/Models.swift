import Foundation

// MARK: - Enums

/// How the daemon learns that a job started and stopped.
enum DetectionMode: String, LenientRawEnum {
    /// Exact: the job is wrapped in `goguma-mark`, which pings the daemon on
    /// start and exit. True duration from run one, plus a real exit code.
    case mark
    /// Best-effort: the daemon scans the process table for a regexp match.
    case pattern
    /// No detection at all: wake, hold for a fixed window, release.
    ///
    /// Spelled `wakeOnly` rather than `none` because a case literally named
    /// `none` collides with `Optional.none` wherever a `DetectionMode?` is in
    /// scope, which is most of the decoding layer. The wire value is `"none"`.
    ///
    /// This is the mode auto-adopted jobs use, and therefore the most common
    /// one. It exists for jobs that run inside another application's own
    /// process (an agent's internal scheduler, n8n, a self-hosted runner)
    /// where there is no command line to wrap and no distinct process to match.
    /// Nothing about it is an error state.
    case wakeOnly = "none"
    case unknown

    static var unknownCase: DetectionMode { .unknown }

    /// What goguma does, not what the mechanism is called.
    ///
    /// These were "Mark", "Pattern" and "Wake only", which name the three
    /// implementations and describe none of them. Reading "Pattern" tells you
    /// nothing unless you already know goguma scans the process table, and
    /// nobody adding a job knows that. The wire values are unchanged; only
    /// what a person sees is.
    var label: String {
        switch self {
        case .mark: "The job reports when it finishes"
        case .pattern: "Watch for the job's process"
        case .wakeOnly: "Stay awake for a set time"
        case .unknown: "Unknown"
        }
    }

    var iconName: String {
        switch self {
        case .mark: Theme.Icon.detectionMark
        case .pattern: Theme.Icon.detectionPattern
        case .wakeOnly: Theme.Icon.detectionNone
        case .unknown: Theme.Icon.outcomeUnknown
        }
    }

    /// Whether this mode can actually see the job run. Mirrors
    /// `model.DetectionMode.Observable()` on the Go side, and is the single
    /// gate for every "we never saw this job" style warning: for a wake-only
    /// job, not seeing it is how it works, not a fault.
    var isObservable: Bool {
        switch self {
        case .mark, .pattern: true
        // An unrecognised mode from a newer daemon is treated as unobservable,
        // so version skew produces silence rather than a wall of false alarms.
        case .wakeOnly, .unknown: false
        }
    }

    /// One line explaining the trade-off, shown under the picker.
    var explanation: String {
        switch self {
        // Kept to two lines at this width. Longer versions left a word or two
        // stranded on a third line, which reads as a layout fault; the detail
        // that was cut lives in Docs/ARCHITECTURE.md, where it is asked for.
        case .mark:
            "Exact. Wrap the job in `goguma-mark` and it reports its own start, "
                + "exit and exit code."
        case .pattern:
            "Best effort. goguma watches the process table for a match. "
                + "Nothing to change, but a wrong pattern is never seen."
        case .wakeOnly:
            "Not watched. goguma holds sleep off for a fixed window, then lets go. "
                + "For jobs that run inside another app, with no process to watch."
        case .unknown:
            "The background service uses a detection mode this version of the app doesn't recognise."
        }
    }

    /// The modes a user may choose. `unknown` only ever arrives from a newer
    /// daemon and is never offered.
    static let selectable: [DetectionMode] = [.mark, .pattern, .wakeOnly]
}

/// How a job's wake window ended.
enum RunOutcome: String, LenientRawEnum {
    case ok
    case failed
    case ceiling
    case neverDetected = "never_detected"
    case cutout
    /// The fire time passed with the Mac asleep and no window opened, so the
    /// job did not run. Recorded rather than left as a gap in the list.
    case slept
    case unknown

    static var unknownCase: RunOutcome { .unknown }

    var label: String {
        switch self {
        case .ok: "OK"
        case .failed: "Failed"
        case .ceiling: "Hit ceiling"
        case .neverDetected: "Never detected"
        case .cutout: "Cutout"
        case .slept: "Missed"
        case .unknown: "Unknown"
        }
    }

    var iconName: String {
        switch self {
        case .ok: Theme.Icon.outcomeOK
        case .failed: Theme.Icon.outcomeFailed
        case .ceiling: Theme.Icon.outcomeCeiling
        case .neverDetected: Theme.Icon.outcomeNeverDetected
        case .cutout: Theme.Icon.outcomeCutout
        case .slept: Theme.Icon.outcomeNeverDetected
        case .unknown: Theme.Icon.outcomeUnknown
        }
    }

    var isHealthy: Bool { self == .ok }
}

/// Which safety valve fired.
enum CutoutKind: String, LenientRawEnum {
    case thermal
    case lowBattery = "low_battery"
    case unknown

    static var unknownCase: CutoutKind { .unknown }

    var label: String {
        switch self {
        case .thermal: "thermal cutout"
        case .lowBattery: "low-battery cutout"
        case .unknown: "safety cutout"
        }
    }
}

/// Classification of a daemon-detected configuration problem.
enum WarningKind: String, LenientRawEnum {
    case neverDetected = "never_detected"
    case wakeFailed = "wake_failed"
    case helperDown = "helper_down"
    case ceilingHits = "ceiling_hits"
    case scheduleParse = "schedule_parse"
    case commandChanged = "command_changed"
    case uncovered
    case retired
    case unknown

    static var unknownCase: WarningKind { .unknown }

    /// Whether this warning means jobs are being missed right now, as opposed
    /// to something merely worth tuning.
    var isCritical: Bool {
        switch self {
        // `uncovered` is scheduled work the machine is sleeping through
        // with no coverage at all; the daemon calls silence here its worst
        // failure. Decoding it as `.unknown` demoted it below every other
        // warning and off the popover's visible rows.
        case .neverDetected, .wakeFailed, .scheduleParse, .uncovered: true
        // `retired` is news, not a fault: a job left its scheduler, which is
        // usually because someone deleted it on purpose. It has to be seen,
        // which is why it is a warning at all, but tinting it as critical
        // would put a red mark on a normal Tuesday.
        case .helperDown, .ceilingHits, .commandChanged, .retired, .unknown: false
        }
    }
}

// MARK: - Job

/// A registered schedule that goguma holds a wake window for. goguma
/// never executes a job; the user's existing cron/launchd entry does that.
struct Job: Codable, Sendable, Hashable, Identifiable {
    var id: String
    var name: String
    var schedule: String
    var tz: String
    var detection: DetectionMode
    /// The wire string `detection` was decoded from, kept so a full-Job
    /// round trip re-encodes a mode this build doesn't know instead of the
    /// literal "unknown". Empty for locally constructed jobs.
    var rawDetection: String = ""
    var match: String
    var command: String
    var source: String

    /// Optional folder name. Flat and free-text; empty means ungrouped.
    var group: String

    var maxRuntime: WGDuration
    var wakeBuffer: WGDuration
    var enabled: Bool

    /// True when goguma adopted this job automatically from a scheduler it
    /// watches, rather than a human registering it.
    ///
    /// It changes what removal means: a managed job is re-adopted on the next
    /// sync, and is retired on its own when it disappears from its source. So
    /// deleting one is close to a no-op, and disabling is what the user
    /// actually wants.
    var managed: Bool

    var createdAt: WGDate?

    init(
        id: String = "",
        name: String = "",
        schedule: String = "",
        tz: String = "",
        detection: DetectionMode = .mark,
        match: String = "",
        command: String = "",
        source: String = "manual",
        group: String = "",
        maxRuntime: WGDuration = .zero,
        wakeBuffer: WGDuration = .zero,
        enabled: Bool = true,
        managed: Bool = false,
        createdAt: WGDate? = nil
    ) {
        self.id = id
        self.name = name
        self.schedule = schedule
        self.tz = tz
        self.detection = detection
        self.match = match
        self.command = command
        self.source = source
        self.group = group
        self.maxRuntime = maxRuntime
        self.wakeBuffer = wakeBuffer
        self.enabled = enabled
        self.managed = managed
        self.createdAt = createdAt
    }

    enum CodingKeys: String, CodingKey {
        case id, name, schedule, tz, detection, match, command, source, group
        case maxRuntime = "max_runtime"
        case wakeBuffer = "wake_buffer"
        case enabled, managed
        case createdAt = "created_at"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.value(.id, "")
        name = c.value(.name, "")
        schedule = c.value(.schedule, "")
        tz = c.value(.tz, "")
        detection = c.value(.detection, DetectionMode.unknown)
        // Kept verbatim for re-encode. The lenient decode maps a mode this
        // build does not know to `.unknown`; writing the literal string
        // "unknown" back on a full-Job round trip (Move to Group does one)
        // was rejected by the daemon's Validate with an error about a value
        // the app itself fabricated.
        rawDetection = c.value(.detection, detection.rawValue)
        match = c.value(.match, "")
        command = c.value(.command, "")
        source = c.value(.source, "")
        group = c.value(.group, "")
        maxRuntime = c.value(.maxRuntime, WGDuration.zero)
        wakeBuffer = c.value(.wakeBuffer, WGDuration.zero)
        enabled = c.value(.enabled, true)
        // Absent on an older daemon, and `omitempty` on the Go side means it is
        // also absent whenever false. Both read as "the user added this".
        managed = c.value(.managed, false)
        createdAt = c.optional(.createdAt)
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(name, forKey: .name)
        try c.encode(schedule, forKey: .schedule)
        try c.encode(tz, forKey: .tz)
        try c.encode(detection == .unknown && !rawDetection.isEmpty
            ? rawDetection : detection.rawValue, forKey: .detection)
        try c.encode(match, forKey: .match)
        try c.encode(command, forKey: .command)
        try c.encode(source, forKey: .source)
        try c.encode(group, forKey: .group)
        try c.encode(maxRuntime, forKey: .maxRuntime)
        try c.encode(wakeBuffer, forKey: .wakeBuffer)
        try c.encode(enabled, forKey: .enabled)
        // Round-tripped rather than assumed: editing a managed job and sending
        // `managed: false` back would quietly un-adopt it, and the next sync
        // would then register a duplicate.
        try c.encode(managed, forKey: .managed)
        // Only send a creation timestamp we actually received; the daemon fills
        // one in for a new job, and inventing one here would be a lie.
        try c.encodeIfPresent(createdAt, forKey: .createdAt)
    }

    /// The name shown in UI, never empty.
    var displayName: String {
        if !name.isEmpty { return name }
        if !id.isEmpty { return id }
        return "Untitled job"
    }

    /// Converts a human name into the filesystem- and IPC-safe id the daemon
    /// uses. A direct port of `model.Slug` in the Go source, because history
    /// files are named after this and a mismatch would orphan them: lower-case,
    /// keep `a-z0-9._`, collapse everything else to a single hyphen, trim.
    static func slug(from name: String) -> String {
        var out = ""
        var lastWasDash = true // suppress a leading dash
        for character in name.trimmingCharacters(in: .whitespaces).lowercased() {
            if character.isASCII, character.isLetter || character.isNumber {
                out.append(character)
                lastWasDash = false
            } else if character == "." || character == "_" {
                out.append(character)
                lastWasDash = false
            } else if !lastWasDash {
                out.append("-")
                lastWasDash = true
            }
        }
        // Trim the same set Go does (`strings.Trim(…, "-._")`): a leading
        // dot would name a hidden history file, and a trimmed-underscore
        // edge keeps the `__name__` shape reserved for internal ids. Only
        // trimming "-" here let the app mint ids the CLI can never produce,
        // splitting one name into two identities with orphaned history.
        let trimSet = CharacterSet(charactersIn: "-._")
        return out.trimmingCharacters(in: trimSet)
    }
}

// MARK: - Run

/// One observed execution of a job.
struct Run: Codable, Sendable, Hashable, Identifiable {
    var jobID: String
    var windowOpened: WGDate?
    var started: WGDate?
    var ended: WGDate?
    var duration: WGDuration
    var holdDuration: WGDuration
    var outcome: RunOutcome
    var exitCode: Int?
    var detection: DetectionMode
    var ceiling: WGDuration
    var wokeMachine: Bool
    /// Charge at each end of the hold, or -1 when there was nothing to measure
    /// (on AC, or unread). -1 must never be read as a flat battery.
    var batteryStart: Int
    var batteryEnd: Int
    var pid: Int

    /// Stable within a job's history: a run is identified by when its window
    /// opened. Falls back to the start time, then to a synthesised value, so a
    /// list never collapses rows because two share an id.
    var id: String {
        let stamp = windowOpened?.date.timeIntervalSince1970
            ?? started?.date.timeIntervalSince1970
            ?? 0
        return "\(jobID)@\(stamp)@\(pid)"
    }

    enum CodingKeys: String, CodingKey {
        case jobID = "job_id"
        case windowOpened = "window_opened"
        case started, ended, duration
        case holdDuration = "hold_duration"
        case outcome
        case exitCode = "exit_code"
        case detection, ceiling
        case wokeMachine = "woke_machine"
        case batteryStart = "battery_start"
        case batteryEnd = "battery_end"
        case pid
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = c.value(.jobID, "")
        windowOpened = c.optional(.windowOpened)
        started = c.optional(.started)
        ended = c.optional(.ended)
        duration = c.value(.duration, WGDuration.zero)
        holdDuration = c.value(.holdDuration, WGDuration.zero)
        outcome = c.value(.outcome, RunOutcome.unknown)
        exitCode = c.optional(.exitCode)
        detection = c.value(.detection, DetectionMode.unknown)
        ceiling = c.value(.ceiling, WGDuration.zero)
        wokeMachine = c.value(.wokeMachine, false)
        batteryStart = c.value(.batteryStart, -1)
        batteryEnd = c.value(.batteryEnd, -1)
        pid = c.value(.pid, 0)
    }

    /// Charge this run cost, in percentage points.
    ///
    /// Zero unless both ends were measured on battery and the level actually
    /// fell. Replaces a derived "wasted hold time": that was a proxy for the
    /// cost, and this is the cost, in the unit the user pays it in.
    var drained: Int {
        guard batteryStart >= 0, batteryEnd >= 0 else { return 0 }
        return max(0, batteryStart - batteryEnd)
    }

    /// Whether this run's cost could be measured at all. Distinguishes "cost
    /// nothing, it was plugged in" from "nothing was measured".
    var measuredOnBattery: Bool { batteryStart >= 0 && batteryEnd >= 0 }

    /// True when a single run cost enough charge to be worth noticing.
    ///
    /// One percent is the smallest a battery reports, so it is also the
    /// smallest honest threshold: below it the answer is "not measurably".
    var isCostly: Bool { drained >= 1 }

    /// The timestamp a history row is ordered and labelled by.
    var timestamp: Date? {
        started.meaningful ?? windowOpened.meaningful ?? ended.meaningful
    }
}

// MARK: - Stats

/// The rolled-up view of a job's history.
struct Stats: Codable, Sendable, Hashable {
    var jobID: String
    var runs: Int
    var typical: WGDuration
    var p95: WGDuration
    var ceiling: WGDuration
    var coldStart: Bool
    var batteryPerRun: Double
    /// Runs that happened because goguma woke the Mac, and fire times that
    /// passed with it asleep. The tool's own scoreboard.
    var woken: Int
    var slept: Int
    var lastRun: Run?
    var failures: Int
    var ceilingHits: Int
    var neverDetected: Int
    var recent: [Run]

    init() {
        jobID = ""
        runs = 0
        typical = .zero
        p95 = .zero
        ceiling = .zero
        coldStart = false
        batteryPerRun = 0
        woken = 0
        slept = 0
        lastRun = nil
        failures = 0
        ceilingHits = 0
        neverDetected = 0
        recent = []
    }

    enum CodingKeys: String, CodingKey {
        case jobID = "job_id"
        case runs, typical, p95, ceiling
        case coldStart = "cold_start"
        case batteryPerRun = "battery_per_run"
        case woken, slept
        case lastRun = "last_run"
        case failures
        case ceilingHits = "ceiling_hits"
        case neverDetected = "never_detected"
        case recent
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = c.value(.jobID, "")
        runs = c.value(.runs, 0)
        typical = c.value(.typical, WGDuration.zero)
        p95 = c.value(.p95, WGDuration.zero)
        ceiling = c.value(.ceiling, WGDuration.zero)
        coldStart = c.value(.coldStart, false)
        batteryPerRun = c.value(.batteryPerRun, 0)
        woken = c.value(.woken, 0)
        slept = c.value(.slept, 0)
        lastRun = c.optional(.lastRun)
        failures = c.value(.failures, 0)
        ceilingHits = c.value(.ceilingHits, 0)
        neverDetected = c.value(.neverDetected, 0)
        recent = c.value(.recent, [Run]())
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(jobID, forKey: .jobID)
        try c.encode(runs, forKey: .runs)
        try c.encode(typical, forKey: .typical)
        try c.encode(p95, forKey: .p95)
        try c.encode(ceiling, forKey: .ceiling)
        try c.encode(coldStart, forKey: .coldStart)
        try c.encode(batteryPerRun, forKey: .batteryPerRun)
        try c.encode(woken, forKey: .woken)
        try c.encode(slept, forKey: .slept)
        try c.encodeIfPresent(lastRun, forKey: .lastRun)
        try c.encode(failures, forKey: .failures)
        try c.encode(ceilingHits, forKey: .ceilingHits)
        try c.encode(neverDetected, forKey: .neverDetected)
        try c.encode(recent, forKey: .recent)
    }
}

// MARK: - Status

/// One active sleep-block.
struct Hold: Codable, Sendable, Hashable, Identifiable {
    var jobID: String
    var jobName: String
    var reason: String
    var openedAt: WGDate?
    var startedAt: WGDate?
    var deadline: WGDate?
    var ceiling: WGDuration
    var detected: Bool
    var detection: DetectionMode
    var pid: Int

    var id: String { jobID.isEmpty ? "hold-\(pid)" : jobID }

    var displayName: String {
        if !jobName.isEmpty { return jobName }
        if !jobID.isEmpty { return jobID }
        return "a job"
    }

    /// The reserved id the daemon opens a manual keep-awake window under.
    /// Mirrors `model.KeepAwakeJobID`.
    static let keepAwakeJobID = "__keep_awake__"

    /// True for a hold the user asked for directly rather than one a job
    /// caused. Almost every phrase about a hold assumes a job is running, and
    /// none of them are true here.
    var isManual: Bool { jobID == Self.keepAwakeJobID }

    /// The reserved id prefix `goguma run` opens a hold under, one per wrapped
    /// command. Mirrors `model.RunHoldPrefix`.
    static let runHoldPrefix = "__run__:"

    /// True for a hold around a command `goguma run` is wrapping.
    ///
    /// Distinct from `isManual`, which the daemon also counts these as: that
    /// governs run history and sleep-back, while this governs wording. A
    /// wrapped command genuinely is running, so the manual phrasing ("you asked
    /// for this, until 14:30") is as wrong for it as the job phrasing is.
    ///
    /// It matters most for the deadline. A run hold's is a 90-second lease
    /// renewed for as long as the process lives, so rendering it as "ceiling in
    /// 1m 27s" counts down to a moment that will not arrive and reads as a hold
    /// about to lapse under a command with an hour left.
    var isWrappedCommand: Bool { jobID.hasPrefix(Self.runHoldPrefix) }

    enum CodingKeys: String, CodingKey {
        case jobID = "job_id"
        case jobName = "job_name"
        case reason
        case openedAt = "opened_at"
        case startedAt = "started_at"
        case deadline, ceiling, detected, detection, pid
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobID = c.value(.jobID, "")
        jobName = c.value(.jobName, "")
        reason = c.value(.reason, "")
        openedAt = c.optional(.openedAt)
        startedAt = c.optional(.startedAt)
        deadline = c.optional(.deadline)
        ceiling = c.value(.ceiling, WGDuration.zero)
        detected = c.value(.detected, false)
        detection = c.value(.detection, DetectionMode.unknown)
        pid = c.value(.pid, 0)
    }

    /// How long this hold has been blocking sleep, as of `now`.
    func elapsed(at now: Date) -> WGDuration {
        guard let opened = openedAt.meaningful else { return .zero }
        return WGDuration(seconds: max(0, now.timeIntervalSince(opened)))
    }

    /// How long until the ceiling force-releases this hold.
    func remaining(at now: Date) -> WGDuration? {
        guard let deadline = deadline.meaningful else { return nil }
        return WGDuration(seconds: deadline.timeIntervalSince(now))
    }
}

/// The machine context the safety cutouts act on.
struct PowerState: Codable, Sendable, Hashable {
    var lidClosed: Bool
    var onAC: Bool
    var batteryPct: Int
    var cpuTempC: Double?
    var thermalSource: String

    init() {
        lidClosed = false
        onAC = false
        batteryPct = 0
        cpuTempC = nil
        thermalSource = ""
    }

    enum CodingKeys: String, CodingKey {
        case lidClosed = "lid_closed"
        case onAC = "on_ac"
        case batteryPct = "battery_pct"
        case cpuTempC = "cpu_temp_c"
        case thermalSource = "thermal_source"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        lidClosed = c.value(.lidClosed, false)
        onAC = c.value(.onAC, false)
        batteryPct = c.value(.batteryPct, 0)
        cpuTempC = c.optional(.cpuTempC)
        thermalSource = c.value(.thermalSource, "")
    }
}

/// A fired safety release. Latches until the hazard genuinely recedes, because
/// the underlying job is usually still running and would re-acquire instantly.
struct Cutout: Codable, Sendable, Hashable {
    var kind: CutoutKind
    var firedAt: WGDate?
    var detail: String
    var latched: Bool
    var releasedHolds: Int

    enum CodingKeys: String, CodingKey {
        case kind
        case firedAt = "fired_at"
        case detail, latched
        case releasedHolds = "released_holds"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = c.value(.kind, CutoutKind.unknown)
        firedAt = c.optional(.firedAt)
        detail = c.value(.detail, "")
        latched = c.value(.latched, false)
        releasedHolds = c.value(.releasedHolds, 0)
    }
}

/// A user-actionable problem, carrying the literal command that fixes it.
struct DaemonWarning: Codable, Sendable, Hashable, Identifiable {
    var kind: WarningKind
    var jobID: String
    var message: String
    var fix: String

    var id: String { "\(kind.rawValue)|\(jobID)|\(message)" }

    enum CodingKeys: String, CodingKey {
        case kind
        case jobID = "job_id"
        case message, fix
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = c.value(.kind, WarningKind.unknown)
        jobID = c.value(.jobID, "")
        message = c.value(.message, "")
        fix = c.value(.fix, "")
    }

    /// For the one warning the app synthesises itself: a wake the system
    /// refused, which the daemon reports through `wake_error` rather than the
    /// warnings array. Everything else here is decoded.
    init(kind: WarningKind, jobID: String, message: String, fix: String) {
        self.kind = kind
        self.jobID = jobID
        self.message = message
        self.fix = fix
    }
}

/// The full daemon snapshot.
struct DaemonStatus: Codable, Sendable, Hashable {
    var schemaVersion: Int
    var daemonRunning: Bool
    var daemonVersion: String
    var holding: Bool
    var holds: [Hold]
    var nextWake: WGDate?
    var nextJob: String
    var nextFire: WGDate?
    var wakeScheduled: Bool
    var wakeError: String

    /// Non-empty when goguma is deliberately not registering a wake, a
    /// safeguard working, not a scheduler failing. Currently set when the
    /// battery is too low for waking to be worth it. Already user-facing prose,
    /// so it is rendered verbatim.
    var wakeSuppressed: String
    /// Why nothing is scheduled, when the reason is not the battery.
    var noWakeReason = ""

    /// When a manual keep-awake window ends, if one is open.
    var keepAwakeUntil: WGDate?

    var helperConnected: Bool
    var helperVersion: String
    var sleepBlocked: Bool
    var power: PowerState
    var paused: Bool
    /// The daemon has not finished its first tick, so nothing has been measured
    /// and no wake computed. Absences, not findings; see `starting` in the Go
    /// model. Defaults false so an older daemon reads as "already running".
    var starting = false
    var cutout: Cutout?
    var lastRun: Run?
    var warnings: [DaemonWarning]

    enum CodingKeys: String, CodingKey {
        case starting
        case schemaVersion = "schema_version"
        case daemonRunning = "daemon_running"
        case daemonVersion = "daemon_version"
        case holding, holds
        case nextWake = "next_wake"
        case nextJob = "next_job"
        case nextFire = "next_fire"
        case keepAwakeUntil = "keep_awake_until"
        case wakeScheduled = "wake_scheduled"
        case wakeError = "wake_error"
        case wakeSuppressed = "wake_suppressed"
        case noWakeReason = "no_wake_reason"
        case helperConnected = "helper_connected"
        case helperVersion = "helper_version"
        case sleepBlocked = "sleep_blocked"
        case power, paused, cutout
        case lastRun = "last_run"
        case warnings
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        schemaVersion = c.value(.schemaVersion, 0)
        daemonRunning = c.value(.daemonRunning, true)
        daemonVersion = c.value(.daemonVersion, "")
        holding = c.value(.holding, false)
        holds = c.value(.holds, [Hold]())
        nextWake = c.optional(.nextWake)
        nextJob = c.value(.nextJob, "")
        nextFire = c.optional(.nextFire)
        wakeScheduled = c.value(.wakeScheduled, false)
        wakeError = c.value(.wakeError, "")
        wakeSuppressed = c.value(.wakeSuppressed, "")
        noWakeReason = c.value(.noWakeReason, "")
        keepAwakeUntil = c.optional(.keepAwakeUntil)
        helperConnected = c.value(.helperConnected, false)
        helperVersion = c.value(.helperVersion, "")
        sleepBlocked = c.value(.sleepBlocked, false)
        power = c.value(.power, PowerState())
        paused = c.value(.paused, false)
        cutout = c.optional(.cutout)
        lastRun = c.optional(.lastRun)
        warnings = c.value(.warnings, [DaemonWarning]())
    }

    func encode(to encoder: any Encoder) throws {
        // The app never sends a status; encoding exists only so the type can
        // satisfy Codable uniformly.
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(schemaVersion, forKey: .schemaVersion)
        try c.encode(daemonRunning, forKey: .daemonRunning)
        try c.encode(holding, forKey: .holding)
        try c.encode(paused, forKey: .paused)
    }

    /// The hold to show when the popover has room for exactly one.
    var primaryHold: Hold? {
        holds.min { lhs, rhs in
            (lhs.openedAt?.date ?? .distantFuture) < (rhs.openedAt?.date ?? .distantFuture)
        }
    }

    /// Warnings to put in front of the user, most severe first.
    ///
    /// Lives on the model rather than the view model so the one piece of real
    /// judgement here (when a missing wake is a failure and when it is a
    /// safeguard) is testable without standing up a UI.
    var actionableWarnings: [DaemonWarning] {
        var out = warnings

        // The daemon reports a failed wake inside `warnings` itself: a
        // `wake_failed` when the helper is up, or only the `helper_down`
        // cause when it is not (deliberately, so the symptom does not sit
        // above its cause with the wrong fix attached). Synthesizing another
        // entry from `wake_error` on top of that showed one fault twice.
        // The synthesis is kept only for daemons old enough to leave both
        // kinds out of the array.
        //
        // And only when it is actually a failure. A non-empty
        // `wake_suppressed` is the daemon saying "I chose not to schedule
        // one", and dressing that up as an error would train users to
        // ignore this list.
        let daemonCoversWake = warnings.contains {
            $0.kind == .wakeFailed || $0.kind == .helperDown
        }
        if !daemonCoversWake, wakeSuppressed.isEmpty,
           !wakeError.isEmpty || (!wakeScheduled && nextWake.meaningful != nil)
        {
            out.insert(
                DaemonWarning(
                    kind: .wakeFailed,
                    jobID: nextJob,
                    message: wakeError.isEmpty
                        ? "The system didn't accept a wake for the next job, so it won't run."
                        : wakeError,
                    fix: ""
                ),
                at: 0
            )
        }

        return out.sorted { lhs, rhs in
            lhs.kind.isCritical && !rhs.kind.isCritical
        }
    }
}

// MARK: - jobs.list

/// A job joined with everything a row needs, so a list is one round trip.
struct JobView: Codable, Sendable, Hashable, Identifiable {
    var job: Job
    var nextFire: WGDate?
    var nextWake: WGDate?
    var scheduleDisplay: String
    var scheduleShort: String
    var scheduleError: String
    var stats: Stats
    var ceilingReason: String
    var holding: Bool

    /// Firings across an unattended night, and what they are projected to
    /// cost. Reported by the daemon, which has the schedule parser; zero cost
    /// means never measured on battery rather than free.
    var firesPerNight: Int
    var nightlyBatteryPct: Double

    var id: String { job.id }

    /// Whether there is a real measurement behind the projection.
    var hasMeasuredCost: Bool { stats.batteryPerRun > 0 }

    /// What one firing costs, or a dash where nothing has been measured.
    var perRunCostText: String {
        guard hasMeasuredCost else { return Format.empty }
        return stats.batteryPerRun < 0.1
            ? "<0.1%"
            : String(format: "%.1f%%", stats.batteryPerRun)
    }

    /// The overnight cost, or a dash where nothing has been measured.
    ///
    /// Never "0.0%". A job that has not yet run on battery costs an unknown
    /// amount, and printing zero for it would put the cheapest-looking number
    /// in the column against the job nobody has evidence about.
    var nightlyCostText: String {
        guard hasMeasuredCost, nightlyBatteryPct > 0 else { return Format.empty }
        return nightlyBatteryPct < 0.1
            ? "<0.1%"
            : String(format: "%.1f%%", nightlyBatteryPct)
    }

    /// Whether this job's overnight cost is large enough to point at.
    ///
    /// 5% of a battery to get through one night is where this stops being
    /// rounding and starts being a morning with less charge.
    var nightlyCostIsHeavy: Bool { hasMeasuredCost && nightlyBatteryPct >= 5 }

    var nightlyCostHelp: String {
        guard hasMeasuredCost else {
            return "This job hasn't run on battery yet, so there is nothing measured to project."
        }
        return "\(firesPerNight) firings across 8 hours asleep, at "
            + String(format: "%.1f%%", stats.batteryPerRun) + " each."
    }

    enum CodingKeys: String, CodingKey {
        case job
        case nextFire = "next_fire"
        case nextWake = "next_wake"
        case scheduleDisplay = "schedule_display"
        case scheduleShort = "schedule_short"
        case scheduleError = "schedule_error"
        case stats
        case ceilingReason = "ceiling_reason"
        case firesPerNight = "fires_per_night"
        case nightlyBatteryPct = "nightly_battery_pct"
        case holding
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        job = c.value(.job, Job())
        nextFire = c.optional(.nextFire)
        nextWake = c.optional(.nextWake)
        scheduleDisplay = c.value(.scheduleDisplay, "")
        scheduleShort = c.value(.scheduleShort, "")
        scheduleError = c.value(.scheduleError, "")
        stats = c.value(.stats, Stats())
        ceilingReason = c.value(.ceilingReason, "")
        firesPerNight = c.value(.firesPerNight, 0)
        nightlyBatteryPct = c.value(.nightlyBatteryPct, 0)
        holding = c.value(.holding, false)
    }

    func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(job, forKey: .job)
        try c.encode(scheduleDisplay, forKey: .scheduleDisplay)
        try c.encode(stats, forKey: .stats)
        try c.encode(holding, forKey: .holding)
    }

    /// Whatever the schedule is best described as: the daemon's human rendering
    /// when it has one, otherwise the raw expression.
    var scheduleText: String {
        scheduleDisplay.isEmpty ? job.schedule : scheduleDisplay
    }

    /// The schedule for compact places: a list row, a narrow column.
    ///
    /// "twice daily" rather than "twice daily, at 09:00 and 21:00". The clock
    /// times are redundant beside a "next run" value that already gives the
    /// actual next time, and repeating them turned every row into a wall of
    /// digits with nothing to catch the eye. The full form stays on the detail
    /// pane, where there is room and nothing to compete with.
    var scheduleShortText: String {
        scheduleShort.isEmpty ? scheduleText : scheduleShort
    }

    /// Did it work last time?
    ///
    /// **`notWatched` is the important case.** A wake-only job records a run
    /// when its wake window opens and closes, and that run's outcome is `ok`,
    /// but `ok` there means "goguma held a window and closed it cleanly",
    /// not "the job succeeded". goguma never sees a wake-only job at all, so
    /// it cannot know. Painting those green would invent information, on the
    /// most common kind of job there is, in exactly the product whose reason to
    /// exist is preventing silent misinformation.
    ///
    /// So green and red are reserved for jobs that are actually observed, and
    /// everything else says plainly that nothing is being watched.
    var runStatus: RunStatus {
        if holding { return .running }
        guard job.detection.isObservable else { return .notWatched }
        guard let last = stats.lastRun else { return .neverRun }
        switch last.outcome {
        case .ok: return .succeeded
        // `slept` sits with the failures, and it is the only one here that is
        // not the job's fault: the Mac was asleep at its fire time and goguma
        // did not wake it. That is still a run that did not happen, which is
        // exactly what this dot is for.
        case .failed, .neverDetected, .cutout, .slept: return .failed
        case .ceiling: return .succeeded
        case .unknown: return .neverRun
        }
    }

    /// Problems worth a badge on this row.
    ///
    /// Everything derived from *observing* the job is gated on the detection
    /// mode being able to observe it. A wake-only job is never seen running by
    /// design; reporting that as a fault would put a warning on every
    /// auto-adopted job on the machine, which is most of them.
    var rowWarnings: [String] {
        var out: [String] = []
        if !scheduleError.isEmpty {
            out.append("Schedule doesn't parse: \(scheduleError)")
        }
        guard job.detection.isObservable else { return out }

        if stats.neverDetected > 0 {
            out.append(
                "\(stats.neverDetected) run\(stats.neverDetected == 1 ? "" : "s") never detected, "
                    + "the match pattern probably doesn't match this job"
            )
        }
        if stats.coldStart {
            out.append("not enough history yet, using the default ceiling")
        }
        if stats.ceilingHits > 0 {
            out.append(
                "\(stats.ceilingHits) run\(stats.ceilingHits == 1 ? "" : "s") hit the ceiling and were cut short"
            )
        }
        return out
    }

    /// The most severe badge tint for this row, if any.
    var worstSeverity: RowSeverity? {
        if !scheduleError.isEmpty { return .critical }
        guard job.detection.isObservable else { return nil }
        if stats.neverDetected > 0 { return .critical }
        if stats.ceilingHits > 0 { return .warning }
        if stats.coldStart { return .info }
        return nil
    }

    /// Whether measured-runtime columns mean anything for this job. False for
    /// wake-only, where `typical` and `p95` stay empty by design.
    var hasMeasuredRuntime: Bool { job.detection.isObservable }
}

/// Severity of a row-level badge, mapped to a colour token in the view layer.
enum RowSeverity: Sendable, Hashable, Comparable {
    // Declaration order IS the severity order (`Comparable` is synthesised
    // from it), so the cases run least to most severe. A collapsed list has to
    // be able to surface the worst of what it hides.
    case info
    case warning
    case critical
}

struct JobsListResponse: Decodable, Sendable {
    var jobs: [JobView]

    /// Every group name in use, sorted, as the daemon sees them. Taken from the
    /// daemon rather than derived from `jobs` so that a group whose last job was
    /// just disabled or removed does not silently vanish from the UI mid-edit.
    var groups: [String]

    enum CodingKeys: String, CodingKey { case jobs, groups }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        jobs = c.value(.jobs, [JobView]())
        groups = c.value(.groups, [String]())
    }
}

// MARK: - history

struct HistoryResponse: Decodable, Sendable {
    var job: Job
    var runs: [Run]
    var scheduleDisplay: String
    var stats: Stats

    enum CodingKeys: String, CodingKey {
        case job, runs, stats
        case scheduleDisplay = "schedule_display"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        job = c.value(.job, Job())
        runs = c.value(.runs, [Run]())
        scheduleDisplay = c.value(.scheduleDisplay, "")
        stats = c.value(.stats, Stats())
    }

    /// What to show a person. Falls back to the raw expression only when the
    /// daemon could not describe it, which is the honest outcome: an ugly
    /// schedule beats a wrong one.
    var scheduleText: String {
        scheduleDisplay.isEmpty ? job.schedule : scheduleDisplay
    }
}

// MARK: - config

/// The daemon's tunables. Every field has a working default on the daemon side,
/// so a partial payload is valid and this decoder never insists on a field.
struct DaemonConfig: Codable, Sendable, Hashable {
    var wakeBuffer: WGDuration
    var defaultCeiling: WGDuration

    /// How long to hold for a job that cannot be observed at all. A fixed
    /// window because there is nothing to measure.
    var wakeOnlyHold: WGDuration

    var minCeiling: WGDuration
    var maxCeiling: WGDuration
    var ceilingMultiplier: Double
    var historyWindow: Int
    var minRunsForEstimate: Int
    var thermalCutoutC: Double
    var lowBatteryCutoutPct: Int

    /// How far the hazard must recede before a latched cutout clears. The
    /// percentage one also sets how much headroom above the battery cutout is
    /// required before the machine will be woken at all, which is why the UI
    /// has to show the derived floor and not just the cutout.
    var cutoutRearmMarginPct: Int
    var cutoutRearmMarginC: Double

    var useWakeOrPowerOn: Bool
    var webhookURL: String
    var notifyOnMissedJob: Bool
    /// Whether goguma fetches its signed notice feed once a day.
    var advisoryChecks: Bool
    var agentHooks: Bool

    /// Whether goguma puts the machine back to sleep after a wake it caused.
    ///
    /// Defaults to true everywhere it is absent, which matters: a daemon too
    /// old to send the key is one that does not have the behaviour either, and
    /// showing the switch off would be right for that daemon but wrong for
    /// every current one. True matches config.Default().
    var sleepAfterWake: Bool

    /// Scheduler sources watched for automatic adoption.
    ///
    /// The three states are distinct and all meaningful:
    /// - `nil`: never configured, which the daemon expands to every adoptable
    ///   source. This is the default and means adoption is ON.
    /// - `[]`: explicitly turned off, and respected as off.
    /// - non-empty: exactly these sources.
    ///
    /// So `nil` must not be collapsed into `[]` anywhere: they are opposites.
    var autoAdopt: [String]?

    var autoAdoptInterval: WGDuration

    init() {
        wakeBuffer = .zero
        defaultCeiling = .zero
        wakeOnlyHold = .zero
        minCeiling = .zero
        maxCeiling = .zero
        ceilingMultiplier = 0
        historyWindow = 0
        minRunsForEstimate = 0
        thermalCutoutC = 0
        lowBatteryCutoutPct = 0
        cutoutRearmMarginPct = 0
        cutoutRearmMarginC = 0
        useWakeOrPowerOn = false
        webhookURL = ""
        notifyOnMissedJob = false
        advisoryChecks = false
        agentHooks = true
        sleepAfterWake = true
        autoAdopt = nil
        autoAdoptInterval = .zero
    }

    enum CodingKeys: String, CodingKey {
        case wakeBuffer = "wake_buffer"
        case defaultCeiling = "default_ceiling"
        case wakeOnlyHold = "wake_only_hold"
        case minCeiling = "min_ceiling"
        case maxCeiling = "max_ceiling"
        case ceilingMultiplier = "ceiling_multiplier"
        case historyWindow = "history_window"
        case minRunsForEstimate = "min_runs_for_estimate"
        case thermalCutoutC = "thermal_cutout_c"
        case lowBatteryCutoutPct = "low_battery_cutout_pct"
        case cutoutRearmMarginPct = "cutout_rearm_margin_pct"
        case cutoutRearmMarginC = "cutout_rearm_margin_c"
        case useWakeOrPowerOn = "use_wake_or_power_on"
        case webhookURL = "webhook_url"
        case notifyOnMissedJob = "notify_on_missed_job"
        case advisoryChecks = "advisory_checks"
        case agentHooks = "agent_hooks"
        case sleepAfterWake = "sleep_after_wake"
        case autoAdopt = "auto_adopt"
        case autoAdoptInterval = "auto_adopt_interval"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        wakeBuffer = c.value(.wakeBuffer, WGDuration.zero)
        defaultCeiling = c.value(.defaultCeiling, WGDuration.zero)
        wakeOnlyHold = c.value(.wakeOnlyHold, WGDuration.zero)
        minCeiling = c.value(.minCeiling, WGDuration.zero)
        maxCeiling = c.value(.maxCeiling, WGDuration.zero)
        ceilingMultiplier = c.value(.ceilingMultiplier, 0)
        historyWindow = c.value(.historyWindow, 0)
        minRunsForEstimate = c.value(.minRunsForEstimate, 0)
        thermalCutoutC = c.value(.thermalCutoutC, 0)
        lowBatteryCutoutPct = c.value(.lowBatteryCutoutPct, 0)
        cutoutRearmMarginPct = c.value(.cutoutRearmMarginPct, 0)
        cutoutRearmMarginC = c.value(.cutoutRearmMarginC, 0)
        useWakeOrPowerOn = c.value(.useWakeOrPowerOn, false)
        webhookURL = c.value(.webhookURL, "")
        notifyOnMissedJob = c.value(.notifyOnMissedJob, false)
        advisoryChecks = c.value(.advisoryChecks, false)
        agentHooks = c.value(.agentHooks, true)
        sleepAfterWake = c.value(.sleepAfterWake, true)
        // `optional` yields nil for both absent and JSON null, and `.some([])`
        // for an empty array, exactly the distinction this field needs.
        autoAdopt = c.optional(.autoAdopt, as: [String].self)
        autoAdoptInterval = c.value(.autoAdoptInterval, WGDuration.zero)
    }

    /// True unless adoption has been explicitly turned off. `nil` is ON.
    var isAutoAdoptEnabled: Bool {
        guard let autoAdopt else { return true }
        return !autoAdopt.isEmpty
    }

    /// The sources named in config, or nil when the daemon is watching
    /// everything adoptable and has not told us which those are.
    var watchedSources: [String]? {
        guard let autoAdopt, !autoAdopt.isEmpty else { return nil }
        return autoAdopt
    }

}

struct ConfigResponse: Decodable, Sendable {
    var config: DaemonConfig
    var warnings: [String]

    /// Charge below which no wake is scheduled for a job whose battery cost
    /// has never been measured.
    ///
    /// Reported by the daemon rather than worked out here. This used to be
    /// `lowBatteryCutoutPct + cutoutRearmMarginPct`, which was the flat-margin
    /// rule from before the floor started rising with a job's own measured
    /// drain; it displayed 15% on a default config where the daemon would
    /// actually wake at 11%. The rearm margin is a different quantity that
    /// governs recovery after a cutout fires.
    var wakeFloorBasePct: Int
    /// Whether the daemon was built with a signing key and can verify a feed.
    var advisoriesAvailable = false

    enum CodingKeys: String, CodingKey {
        case config, warnings
        case wakeFloorBasePct = "wake_floor_base_pct"
        case advisoriesAvailable = "advisories_available"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        config = c.value(.config, DaemonConfig())
        warnings = c.value(.warnings, [String]())
        wakeFloorBasePct = c.value(.wakeFloorBasePct, 0)
        advisoriesAvailable = c.value(.advisoriesAvailable, false)
    }
}

// MARK: - sync

/// Reply to `sync`. Each kind of change is its own non-negative count. The
/// old contract was one signed `added`, which hid a rename entirely (one
/// adoption plus one retirement summed to zero and read "Already up to
/// date." at the exact moment a job vanished from the list); the negative
/// branch below survives only for daemons older than the split.
struct SyncResponse: Decodable, Sendable {
    var added: Int
    var updated: Int
    var retired: Int

    enum CodingKeys: String, CodingKey { case added, updated, retired }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        added = c.value(.added, 0)
        updated = c.value(.updated, 0)
        retired = c.value(.retired, 0)
    }

    /// How to describe the outcome to the user.
    var summary: String {
        if added < 0 {
            // An old daemon's signed delta.
            return "Retired \(Format.count(-added, "job")) that no longer exist in their source."
        }
        var parts: [String] = []
        if added > 0 {
            parts.append("Adopted \(Format.count(added, "new job")).")
        }
        if updated > 0 {
            parts.append("Updated \(Format.count(updated, "job")) that changed in their source.")
        }
        if retired > 0 {
            parts.append("Retired \(Format.count(retired, "job")) that no longer exist in their source.")
        }
        return parts.isEmpty ? "Already up to date." : parts.joined(separator: " ")
    }
}

// MARK: - match.test

struct ProcessMatch: Decodable, Sendable, Hashable, Identifiable {
    var pid: Int
    var command: String

    var id: Int { pid }

    enum CodingKeys: String, CodingKey { case pid, command }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pid = c.value(.pid, 0)
        command = c.value(.command, "")
    }
}

struct MatchTestResponse: Decodable, Sendable {
    var valid: Bool
    var error: String
    var matches: [ProcessMatch]

    enum CodingKeys: String, CodingKey { case valid, error, matches }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        valid = c.value(.valid, false)
        error = c.value(.error, "")
        matches = c.value(.matches, [ProcessMatch]())
    }
}

// MARK: - Request payloads

struct JobRefPayload: Encodable, Sendable {
    var ref: String
}

struct EnablePayload: Encodable, Sendable {
    var ref: String
    var enabled: Bool
}

struct HistoryPayload: Encodable, Sendable {
    var ref: String
    var limit: Int
}

struct ConfigSetPayload: Encodable, Sendable {
    var key: String
    var value: String
}

struct MatchTestPayload: Encodable, Sendable {
    var pattern: String
}


// MARK: - keep_awake

struct KeepAwakeRequest: Encodable, Sendable {
    var duration: WGDuration
    init(for duration: WGDuration) { self.duration = duration }
    enum CodingKeys: String, CodingKey { case duration = "for" }
}

struct KeepAwakeResponse: Decodable, Sendable {
    var active: Bool
    /// Set when the daemon clamped the request into its allowed range, so the
    /// UI can say so rather than silently showing a different number.
    var clamped: Bool
    var until: WGDate?

    enum CodingKeys: String, CodingKey { case active, clamped, until }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        active = c.value(.active, false)
        clamped = c.value(.clamped, false)
        until = c.optional(.until)
    }
}


/// Whether a job worked the last time it ran, as far as goguma can tell.
enum RunStatus: Sendable, Hashable {
    case running
    case succeeded
    case failed
    case neverRun
    /// The job cannot be observed, so no outcome is knowable. See
    /// `JobView.runStatus`.
    case notWatched

    var label: String {
        switch self {
        case .running: "Running now"
        case .succeeded: "Last run succeeded"
        case .failed: "Last run failed"
        case .neverRun: "Hasn't run yet"
        case .notWatched: "Not watched, goguma can't tell"
        }
    }
}

extension Stats {
    /// The per-run cost, worded the way a person would say it.
    ///
    /// One decimal below 10%, none above: 0.3% and 0.4% are meaningfully
    /// different, 12.4% and 12% are not, and decimals on a larger number read
    /// as false precision from a battery that only reports whole percents.
    var batteryPerRunText: String? {
        guard batteryPerRun > 0 else { return nil }
        return batteryPerRun < 10
            ? String(format: "%.1f%%", batteryPerRun)
            : String(format: "%.0f%%", batteryPerRun)
    }
}
