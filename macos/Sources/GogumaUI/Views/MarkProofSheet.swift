import SwiftUI

/// Development-only proof sheet for `MenuBarMark`. Not shipped in any surface.
///
/// Shows the mark at the size it actually renders in the menu bar, beside a
/// blow-up, in the tint it actually gets. 18pt is the only size where a
/// decision here can be judged.
struct MarkProofSheet: View {
    var body: some View {
        HStack(spacing: Theme.Space.xl) {
            // The sweet potato, in full colour: skin outside, flesh showing
            // once it is split. 16pt first, because that is the only size the
            // menu bar ever renders and the only one worth judging.
            ForEach([false, true], id: \.self) { asleep in
                VStack(spacing: Theme.Space.md) {
                    Text(asleep ? "idle · whole" : "holding · split")
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                    Image(nsImage: SweetPotatoMark.image(
                        size: Theme.StatusItem.glyphSize, asleep: asleep,
                        skin: NSColor(Theme.Colors.potatoSkin),
                        flesh: NSColor(Theme.Colors.potatoFlesh)))
                    Image(nsImage: SweetPotatoMark.image(
                        size: 96, asleep: asleep,
                        skin: NSColor(Theme.Colors.potatoSkin),
                        flesh: NSColor(Theme.Colors.potatoFlesh)))
                }
            }
            ForEach([false, true], id: \.self) { asleep in
                VStack(spacing: Theme.Space.md) {
                    Text(asleep ? "idle · asleep" : "holding · awake")
                        .font(Theme.Typography.caption)
                        .foregroundStyle(Theme.Colors.textSecondary)
                    Image(nsImage: MenuBarMark.image(
                        size: Theme.StatusItem.glyphSize, asleep: asleep))
                        .renderingMode(.template)
                        .foregroundStyle(asleep ? Theme.Colors.brandFill : Theme.Colors.ember)
                    Image(nsImage: MenuBarMark.image(size: 96, asleep: asleep))
                        .renderingMode(.template)
                        // The colours the menu bar actually uses: ember while
                        // holding, brand blue at rest. This sheet exists to be
                        // ground truth for the glyph at 16pt, so painting both
                        // states in `accent` made it ground truth for nothing.
                        .foregroundStyle(asleep ? Theme.Colors.brandFill : Theme.Colors.ember)
                }
            }
        }
        .padding(Theme.Space.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .themeSurface()
    }
}
