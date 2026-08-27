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
            // The glyph the menu bar actually draws.
            //
            // First, because it is the only one of these that ships to a status
            // item, and this sheet's whole claim is that 18pt is the size where
            // a decision about the mark can be judged. It was rendering two
            // marks the menu bar no longer uses and not the one it does.
            //
            // Drawn twice against opposite backgrounds: a template is tinted by
            // the system, so the question it has to survive is not "does this
            // look right" but "does this read on a light bar and a dark one".
            VStack(spacing: Theme.Space.md) {
                Text("menu bar · template")
                    .font(Theme.Typography.caption)
                    .foregroundStyle(Theme.Colors.textSecondary)
                ForEach([false, true], id: \.self) { onDark in
                    HStack(spacing: Theme.Space.md) {
                        Image(nsImage: SweetPotatoMark.templateImage(
                            size: Theme.StatusItem.glyphSize))
                            .renderingMode(.template)
                            .foregroundStyle(onDark ? Color.white : Color.black)
                        Image(nsImage: SweetPotatoMark.templateImage(size: 96))
                            .renderingMode(.template)
                            .foregroundStyle(onDark ? Color.white : Color.black)
                    }
                    .padding(Theme.Space.sm)
                    .background(onDark ? Color.black : Color.white)
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
