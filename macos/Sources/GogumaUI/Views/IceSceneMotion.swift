import SwiftUI

// MARK: - Procedural motion
//
// Ported from the reference diorama's motion engine. The animals are not on
// keyframe loops: each picks a target, accelerates toward it, eases to a stop on
// arrival, dwells a random beat, sometimes turns to face the other way, then
// picks a new target. Gait phase advances with actual speed, so legs sync to how
// fast the animal is going.
//
// The reason to do it this way rather than with a repeating animation is that a
// loop is recognisable. Within about two cycles the eye has learned it and the
// illustration goes dead. A scene that never exactly repeats stays alive in
// peripheral vision, which is the whole point of putting it on a pane someone is
// looking at because they have nothing to do.

/// Drives every moving part of `IceScene`. Advanced once per frame from a
/// `TimelineView`; never advanced at all under Reduce Motion.
@Observable
final class IceSceneMotion {
    private(set) var bear = WalkerState()
    private(set) var penguins: [WalkerState]
    private(set) var seal = SurfacerState()
    private(set) var snow: [Flake]

    private var bearWalker = Walker(min: 56, max: 252, maxV: 14, accel: 22, stepFreq: 1.15,
                                    dwellMin: 0.15, dwellMax: 0.9, nearChance: 0.12)
    private var penguinWalkers: [Walker]
    private var surfacer = Surfacer()
    private var lastTick: Date?
    private var elapsed: Double = 0

    /// Overlapping ranges and different speeds, so the trio drifts apart,
    /// crosses over, and mingles rather than moving as a block.
    private static let penguinSpec: [(min: CGFloat, max: CGFloat, scale: CGFloat,
                                      offset: CGFloat, maxV: Double, dwell: ClosedRange<Double>,
                                      near: Double)] = [
        (46, 158, 1.00, 0, 17, 0.15...0.9, 0.18),
        (70, 200, 1.05, 1, 21, 0.12...0.8, 0.16),
        (54, 184, 0.92, -1, 15, 0.18...1.0, 0.22),
    ]

    init() {
        penguinWalkers = Self.penguinSpec.map {
            Walker(min: $0.min, max: $0.max, maxV: $0.maxV, accel: 30, stepFreq: 2.0,
                   dwellMin: $0.dwell.lowerBound, dwellMax: $0.dwell.upperBound,
                   nearChance: $0.near)
        }
        penguins = Self.penguinSpec.map {
            var s = WalkerState()
            s.scale = $0.scale
            s.bottomOffset = $0.offset
            return s
        }
        snow = (0..<22).map { _ in Flake.seeded() }
        // Seed one frame so a Reduce Motion still is a composed scene rather
        // than every animal stacked at x = 0.
        step(dt: 0)
    }

    /// Advances to `date`, returning nothing; the call exists for its effect on
    /// the observable state the canvas reads.
    @discardableResult
    func advance(to date: Date) -> Bool {
        guard let last = lastTick else {
            lastTick = date
            return true
        }
        lastTick = date
        // Clamped, so a stalled or backgrounded frame cannot teleport an animal
        // across the floe when the app comes back.
        step(dt: min(0.05, max(0, date.timeIntervalSince(last))))
        return true
    }

    private func step(dt: Double) {
        elapsed += dt

        bear = bearWalker.update(dt: dt, now: elapsed, into: bear) { state, sway, moving, breath in
            state.bodyLift = -abs(sway) * 1.8 * moving - max(0, breath) * 0.7
            state.bodyTilt = sway * 1.1 * moving + breath * 0.5
            state.frontLeg = sway * 14 * moving
            state.backLeg = -sway * 14 * moving
        }

        for i in penguins.indices {
            penguins[i] = penguinWalkers[i].update(
                dt: dt, now: elapsed, into: penguins[i]
            ) { state, sway, moving, _ in
                // Penguins sway while standing; the idle term is phase-shifted
                // by position so the three are never in step with each other.
                let idle = sin(elapsed * 1.7 + Double(state.x) * 0.1) * (1 - moving)
                state.bodyTilt = sway * (3 + 8 * moving) + idle * 1.4
                state.bodyLift = -abs(sway) * 2.4 * moving - abs(idle) * 0.5
                state.leftFoot = sway * 15 * moving
                state.rightFoot = -sway * 15 * moving
            }
        }

        seal = surfacer.update(dt: dt, now: elapsed, into: seal)

        for i in snow.indices {
            snow[i].advance(dt: dt)
        }
    }
}

// MARK: - State

struct WalkerState {
    var x: CGFloat = 0
    var facing: CGFloat = 1
    var scale: CGFloat = 1
    var bottomOffset: CGFloat = 0

    var bodyLift: CGFloat = 0
    var bodyTilt: Double = 0
    var frontLeg: Double = 0
    var backLeg: Double = 0
    var leftFoot: Double = 0
    var rightFoot: Double = 0
}

struct SurfacerState {
    /// Offset below the waterline. Larger is deeper. Defaults to the peek
    /// depth so the first frame drawn already has the seal up; see `Surfacer`.
    var y: CGFloat = 16
    var rotation: Double = 2
    var headTurn: Double = 0
    var rippleScale: CGFloat = 0
    var rippleOpacity: Double = 0
}

// MARK: - Walker

/// A grounded animal ambling along x between `min` and `max`.
private struct Walker {
    let min: CGFloat
    let max: CGFloat
    let maxV: Double
    let accel: Double
    let stepFreq: Double
    let dwellMin: Double
    let dwellMax: Double
    /// Chance a new target is a short shuffle rather than a stroll across.
    let nearChance: Double

    private var x: CGFloat
    private var v: Double = 0
    private var target: CGFloat
    private var dwell: Double
    private var facing: CGFloat
    private var phase: Double

    init(min: CGFloat, max: CGFloat, maxV: Double, accel: Double, stepFreq: Double,
         dwellMin: Double, dwellMax: Double, nearChance: Double) {
        self.min = min
        self.max = max
        self.maxV = maxV
        self.accel = accel
        self.stepFreq = stepFreq
        self.dwellMin = dwellMin
        self.dwellMax = dwellMax
        self.nearChance = nearChance
        x = CGFloat.random(in: min...max)
        target = CGFloat.random(in: min...max)
        dwell = Double.random(in: 0.2...1.2)
        facing = Bool.random() ? 1 : -1
        phase = Double.random(in: 0..<(2 * .pi))
    }

    private mutating func pickTarget(from: CGFloat) -> CGFloat {
        let span = max - min
        if Double.random(in: 0..<1) < nearChance {
            let near = from + CGFloat.random(in: -0.28...0.28) * span
            return Swift.max(min, Swift.min(max, near))
        }
        return CGFloat.random(in: min...max)
    }

    /// Advances one frame and applies the caller's per-species pose rules.
    ///
    /// - Parameter pose: receives the state, the gait sine, how fast the animal
    ///   is going as 0…1, and a slow breathing term used while it stands still.
    mutating func update(
        dt: Double,
        now: Double,
        into state: WalkerState,
        pose: (inout WalkerState, Double, Double, Double) -> Void
    ) -> WalkerState {
        var desiredV: Double = 0
        if dwell > 0 {
            dwell -= dt
        } else {
            let dx = target - x
            let dist = abs(dx)
            if dist < 1.2 {
                // Arrived: rest, and sometimes turn around before moving on.
                dwell = Double.random(in: dwellMin...dwellMax)
                if Bool.random() { facing = -facing }
                target = pickTarget(from: x)
            } else {
                let arriveR: CGFloat = 26
                let ramp = dist < arriveR ? Double(dist / arriveR) : 1
                desiredV = (dx > 0 ? 1 : -1) * maxV * ramp
            }
        }

        // Accelerate toward the desired velocity, which is what gives the
        // ease-in on departure without any easing curve.
        let dv = desiredV - v
        let step = accel * dt
        v += abs(dv) <= step ? dv : (dv > 0 ? step : -step)

        x += CGFloat(v * dt)
        if x < min { x = min; v = 0 }
        if x > max { x = max; v = 0 }

        let speed = abs(v)
        let moving = maxV > 0 ? speed / maxV : 0
        // A faint shuffle continues when nearly still, so a resting animal is
        // not perfectly frozen.
        phase += (0.4 + (stepFreq * 2 * .pi) * moving) * dt
        // Facing commits only once genuinely walking, so the turn happens at a
        // pause rather than mid-stride.
        if speed > maxV * 0.22 { facing = v > 0 ? 1 : -1 }

        var out = state
        out.x = x
        out.facing = facing
        let breathe = sin(now * 1.05) * (1 - moving)
        pose(&out, sin(phase), moving, breathe)
        return out
    }
}

// MARK: - Surfacer

/// The seal at its breathing hole: a long irregular wait under the ice, a smooth
/// rise to peek, a breathing hold, then a smooth sink.
private struct Surfacer {
    private enum Phase { case under, rise, peek, sink }

    private let deep: CGFloat = 70
    private let peekDepth: CGFloat = 16
    private let riseDuration: Double = 1.15
    private let sinkDuration: Double = 1.0

    // Seeded surfaced, not submerged.
    //
    // Starting `.under` meant the very first frame had no seal in it, and
    // because Reduce Motion renders exactly one frame and never advances, the
    // seal was permanently invisible for anyone with that setting on. The scene
    // silently lost the one animal that carries the product's meaning, for the
    // users least able to spare it. It now opens at the surface and sinks from
    // there, so the still frame is a complete composition.
    private var phase: Phase = .peek
    private var timer: Double = Double.random(in: 1.6...4.2)
    private var progress: Double = 0
    private var firedRipple = false
    private var rippleAge: Double = .infinity

    /// Smootherstep. Gentler at both ends than a cosine, which is what makes the
    /// rise read as a body moving through water rather than a lift.
    private func smooth(_ t: Double) -> Double {
        let t = Swift.max(0, Swift.min(1, t))
        return t * t * t * (t * (t * 6 - 15) + 10)
    }

    mutating func update(dt: Double, now: Double, into state: SurfacerState) -> SurfacerState {
        var out = state
        var splash = false
        timer -= dt
        rippleAge += dt

        switch phase {
        case .under:
            out.y = deep
            out.rotation = -6
            out.headTurn = 0
            if timer <= 0 {
                phase = .rise
                progress = 0
                firedRipple = false
            }
        case .rise:
            progress += dt / riseDuration
            let e = smooth(progress)
            out.y = deep + (peekDepth - deep) * CGFloat(e)
            // Nose-up breaking the surface, levelling off as it arrives.
            out.rotation = -18 + 20 * e
            if progress > 0.45 && !firedRipple {
                splash = true
                firedRipple = true
            }
            if progress >= 1 {
                phase = .peek
                timer = Double.random(in: 1.6...4.2)
            }
        case .peek:
            out.y = peekDepth + CGFloat(sin(now * 1.7) * 1.4)
            out.rotation = 2 + sin(now * 1.1) * 2.5
            out.headTurn = sin(now * 0.8) * 4
            if timer <= 0 {
                phase = .sink
                progress = 0
                firedRipple = false
            }
        case .sink:
            progress += dt / sinkDuration
            let e = smooth(progress)
            out.y = peekDepth + (deep - peekDepth) * CGFloat(e)
            out.rotation = 2 + 14 * e
            if progress > 0.4 && !firedRipple {
                splash = true
                firedRipple = true
            }
            if progress >= 1 {
                phase = .under
                timer = Double.random(in: 3.5...8.0)
            }
        }

        if splash { rippleAge = 0 }

        // One expanding ring per crossing, fading as it spreads.
        let life = 1.15
        if rippleAge < life {
            let t = rippleAge / life
            out.rippleScale = CGFloat(0.4 + 1.1 * t)
            out.rippleOpacity = t < 0.2 ? (t / 0.2) * 0.7 : 0.7 * (1 - (t - 0.2) / 0.8)
        } else {
            out.rippleOpacity = 0
        }
        return out
    }
}

// MARK: - Snow

/// One drifting flake. Wraps to the top rather than being recreated, so the
/// count stays fixed and there is no allocation in the frame loop.
struct Flake: Identifiable {
    let id = UUID()
    var x: CGFloat
    var y: CGFloat
    let size: CGFloat
    let opacity: Double
    let fallSpeed: CGFloat
    let driftSpeed: CGFloat
    let driftPhase: Double

    static func seeded() -> Flake {
        Flake(
            x: CGFloat.random(in: 0...360),
            y: CGFloat.random(in: -20...250),
            size: CGFloat.random(in: 0.75...1.9),
            opacity: Double.random(in: 0.3...0.75),
            fallSpeed: CGFloat.random(in: 14...30),
            driftSpeed: CGFloat.random(in: -9...9),
            driftPhase: Double.random(in: 0..<(2 * .pi))
        )
    }

    mutating func advance(dt: Double) {
        y += fallSpeed * CGFloat(dt)
        x += driftSpeed * CGFloat(dt) * CGFloat(sin(driftPhase + Double(y) * 0.02))
        if y > 262 {
            y = -8
            x = CGFloat.random(in: 0...360)
        }
        if x < -10 { x = 370 }
        if x > 370 { x = -10 }
    }
}
