import SwiftUI

/// The two long-lived objects the app is built from.
///
/// Lazily created statics rather than properties the delegate fills in at
/// launch, because the `MenuBarExtra` scene body is evaluated *before*
/// `applicationDidFinishLaunching` runs. When the coordinator was an optional
/// the delegate assigned later, the scene read `nil`, rendered a placeholder,
/// and never re-evaluated; a plain `static var` is not observable, so nothing
/// ever told SwiftUI the value had arrived. The dropdown was permanently empty.
///
/// A lazy `static let` has no ordering to get wrong: whoever touches it first
/// creates it, and everyone else gets the same instance.
@MainActor
enum AppEnvironment {
    static let store = StatusStore()
    static let coordinator = WindowCoordinator(store: store)
}
