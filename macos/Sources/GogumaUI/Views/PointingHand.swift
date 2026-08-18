import AppKit
import SwiftUI

/// The pointing hand while the pointer is over a control.
///
/// AppKit gives a button no cursor of its own, so goguma's popover and windows
/// left the arrow over every control on them. On a surface that is almost
/// entirely buttons, that reads as a picture of an interface rather than one
/// you can use: nothing under the pointer ever acknowledges it.
///
/// Push and pop rather than `set()`, because `set()` is undone by the next
/// thing that draws and the cursor flickers back to the arrow while the pointer
/// is still over the control.
private struct PointingHandCursor: ViewModifier {
    @State private var pushed = false

    func body(content: Content) -> some View {
        content
            .onHover { inside in
                // Guarded on `pushed` rather than pushing on every callback.
                // `onHover` fires more than once for one entry, and the stack
                // is global: an unbalanced push leaves every window in the app
                // showing a hand until something unrelated resets it.
                if inside, !pushed {
                    pushed = true
                    NSCursor.pointingHand.push()
                } else if !inside, pushed {
                    pushed = false
                    NSCursor.pop()
                }
            }
            .onDisappear {
                // A control can be removed while the pointer is still over it:
                // the popover closes, a row disappears as a job finishes. No
                // exit callback arrives for that, so without this the hand
                // outlives the thing that asked for it.
                if pushed {
                    pushed = false
                    NSCursor.pop()
                }
            }
    }
}

extension View {
    /// Shows the pointing hand over this view.
    ///
    /// For anything that responds to a click. Deliberately not applied to
    /// checkboxes, sliders and pop-up buttons: those are AppKit controls that
    /// carry the platform's own cursor behaviour, and overriding it would make
    /// goguma the one Mac app where a slider claims to be a link.
    func pointingHand() -> some View { modifier(PointingHandCursor()) }
}
