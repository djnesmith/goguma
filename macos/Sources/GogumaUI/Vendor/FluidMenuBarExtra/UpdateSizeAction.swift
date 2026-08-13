//
//  UpdateSizeAction.swift
//  FluidMenuBarExtra
//
//  Created by Lukas Romsicki on 2022-12-17.
//  Copyright © 2022 Lukas Romsicki.
//

import SwiftUI

/// Structure representing an action that is called by a child view to notify a parent view
/// that one of its children has resized.
struct UpdateSizeAction {
    typealias Action = (_ size: CGSize) -> Void

    let action: Action

    func callAsFunction(size: CGSize) {
        action(size)
    }
}

private struct UpdateSizeKey: EnvironmentKey {
    // goguma: was `static var`, which Swift 6 rejects as mutable global
    // state. The value is always nil; it is a default that only ever gets
    // replaced per-view via `.environment`, so `let` is the honest spelling,
    // and `nonisolated(unsafe)` covers the compiler's remaining objection that
    // `UpdateSizeAction` wraps a non-Sendable closure. Nothing mutates it.
    nonisolated(unsafe) static let defaultValue: UpdateSizeAction? = nil
}

extension EnvironmentValues {
    var updateSize: UpdateSizeAction? {
        get { self[UpdateSizeKey.self] }
        set { self[UpdateSizeKey.self] = newValue }
    }
}

extension View {
    /// Adds an action to perform when a child view reports that it has resized.
    /// - Parameter action: The action to perform.
    func onSizeUpdate(_ action: @escaping (_ size: CGSize) -> Void) -> some View {
        let action = UpdateSizeAction { size in
            action(size)
        }

        return environment(\.updateSize, action)
    }
}
