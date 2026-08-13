//
//  FluidMenuBarExtraTypes.swift
//  FluidMenuBarExtra
//
//  Created by Lukas Romsicki on 2022-12-17.
//  Copyright © 2022 Lukas Romsicki.
//
//  Extracted verbatim from the package's FluidMenuBarExtra.swift, which is not
//  vendored: goguma drives the status item from its app delegate rather than
//  through the package's `Scene` wrapper, because the menu bar icon has to
//  change with daemon state and the wrapper sets its image only once at init.
//

/// Controls how the pop-up window is aligned relative to the menubar item.
enum PopUpAlignment: Hashable {
    /// The pop-up window's left edge is aligned with the menubar item's left edge.
    case left

    /// The pop-up window is centred underneath the menubar item.
    case centre

    /// The pop-up window's right edge is aligned with the menubar item's right edge.
    case right
}

/// Controls how the pop-up window's position is adapted to space constraints
/// from encountering the left or right edges of the screen.
enum ScreenClippingBehaviour: Hashable {
    /// If there isn't enough space to use the normal alignment, switch to its
    /// reverse (e.g. `.right` instead of `.left`). If this still isn't
    /// sufficient to resolve the problem, the behaviour falls back to `hugEdge`.
    case reverseAlignment

    /// Nudge the pop-up window in from the edge just enough to make it fully
    /// visible. This may mean an otherwise unnatural alignment of the pop-up
    /// window and the menubar item.
    case hugEdge
}
