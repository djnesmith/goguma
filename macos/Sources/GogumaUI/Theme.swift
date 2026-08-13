import SwiftUI

// MARK: - Theme: snowscroll
//
// THE SINGLE FILE TO EDIT WHEN THE DESIGN CHANGES.
//
// Every visual decision in goguma's menu bar app lives here: colours, type,
// spacing, corner radii, stroke widths, SF Symbol names, surface geometry, and
// motion. No other file in this target hard-codes a `Color`, a `Font`, a
// `Material`, or an icon name; a grep for those outside this file returns
// nothing, and that is a property worth keeping.
//
// The system is **snowscroll**: arctic and calm, one accent, everything else
// quiet neutrals. Its stated principles are restraint and honesty. goguma
// runs unattended and is looked at mainly when someone suspects a problem, so it
// earns trust by staying quiet and raising its voice only when something is
// genuinely wrong. A tool that shouts at every row does not.
//
// This mirrors `internal/render/theme.go`, which does the same job for the CLI.
// The two share the semantic palette by design: "this needs attention" has to
// mean the same thing in a terminal and in the menu bar.

enum Theme {

    // MARK: - Palette
    //
    // Named by *meaning*, never by hue, so a call site asks for "this failed"
    // rather than "make this red", and a future palette can reassign roles
    // without touching a single view.
    //
    // Every role has a light and a dark value and resolves through a dynamic
    // `NSColor`, so appearance changes are picked up live by AppKit without any
    // view observing anything.

    enum Colors {

        // MARK: Brand

        /// The primary highlight: next wake time, the value the eye should land
        /// on, the thing you would click.
        ///
        /// The light value is deliberately **not** snowscroll's brand accent.
        /// That accent, `#8FB6D4`, is a pale arctic blue designed as a *fill*
        /// behind dark text; as foreground on white it lands near 1.9:1 and is
        /// unreadable. Almost every use in this app is text, a line, or a glyph,
        /// so light mode uses the darker `#2E6A8F` (~5.5:1) while dark mode keeps
        /// the brand value, which reads around 6:1 on the dark surface.
        static let accent = sweetPotato ? purpleAccent : adaptive(light: 0x2E6A8F, dark: 0x8FB6D4)

        /// The brand accent as a *fill only*, with dark text on top. This is the
        /// one context in which `#8FB6D4` is correct in light mode. Never use it
        /// as a foreground colour.
        static let brandFill = sweetPotato ? potatoSkin : adaptive(light: 0x8FB6D4, dark: 0x8FB6D4)

        // MARK: Semantic states

        /// Healthy, succeeded, working right now. A pine green rather than a
        /// spring green, desaturated to the same key as the arctic blue so that
        /// "working" reads as calm rather than celebratory.
        static let ok = adaptive(light: 0x2E7D5B, dark: 0x7CC0A0)

        /// Needs attention but is not broken: a cold-start ceiling, a job that
        /// hit its cap, a wake deliberately held back. Deep amber, clearly warm
        /// against an otherwise cool palette, which is what makes it register
        /// without a loud saturation.
        static let warning = adaptive(light: 0xB77A16, dark: 0xE2B45A)

        /// Failed, cut out, will not work. Terracotta rather than fire-engine
        /// red: muted enough to belong to the palette, distinct enough to stop
        /// the reader.
        static let danger = adaptive(light: 0xB23B2E, dark: 0xE28578)

        // MARK: Text

        /// Section titles and headings.
        static let heading = adaptive(light: 0x1C2A33, dark: 0xE9EFF3)
        /// Body copy.
        static let textPrimary = sweetPotato ? purpleTextPrimary : adaptive(light: 0x1C2A33, dark: 0xE9EFF3)
        /// Labels, timestamps, explanations.
        static let textSecondary = sweetPotato ? purpleTextSecondary : adaptive(light: 0x5C6B75, dark: 0x8D9CA7)
        /// Present but deliberately quiet.
        ///
        /// Also the correct role for "there is nothing to show here", which is
        /// why an unobservable job's empty duration column is muted and never
        /// danger. Muted means "nothing to show", which is the truth.
        static let textTertiary = adaptive(light: 0x5C6B75, dark: 0x8D9CA7).opacity(0.75)

        // MARK: Surfaces

        /// App background behind the popover material.
        /// The one switch. `false` restores the arctic world exactly: every
        /// colour below reads from it, so trying the purple costs a line and
        /// reverting costs the same line.
        static let sweetPotato = true

        static let surface = sweetPotato
            ? purpleSurface : adaptive(light: 0xEEF4F8, dark: 0x14191D)
        /// A raised card or panel.
        static let card = sweetPotato ? purpleCard : adaptive(light: 0xFFFFFF, dark: 0x1D242A)
        /// Hairline rules and borders.
        ///
        /// The editorial hairline is the most recognisable part of the system:
        /// sections are separated by these and by space, never by boxes.
        static let divider = sweetPotato ? purpleDivider : adaptive(light: 0xDBE5EC, dark: 0x2A333A)

        /// Alias for `divider`, kept so call sites do not churn.
        static let separator = divider
        /// Fill behind a raised panel. Used sparingly; hairlines are preferred.
        static let cardFill = adaptive(light: 0xFFFFFF, dark: 0x1D242A)

        // MARK: State roles
        //
        // These name application states rather than severities, so the mapping
        // from "what is happening" to "what colour" is decided once, here.

        /// Nothing is holding sleep off. Quiet: this is the normal resting state
        /// and must not draw the eye.
        static let stateIdle = textSecondary
        /// A wake window is open and sleep is being blocked.
        ///
        /// Ember, not the arctic accent. Holding is the only state in which
        /// goguma is *doing* something, and it used to render in the same
        /// blue as idle; the menu bar looked identical whether the machine was
        /// being kept awake or left alone, with only the bear's eyes to tell
        /// them apart. A lit hearth against a cold one says it at a glance.
        ///
        /// The warm half of the wheel is crowded: `warning` sits at 37°/40° and
        /// `danger` at 6°/7°, so ember takes the one balanced slot between them
        /// (~15° from each in both modes) rather than leaning toward either.
        /// It stays off anything thermal for that reason; see `ember`.
        static let stateHolding = ember
        /// The user has paused goguma.
        static let statePaused = textSecondary
        /// A thermal or low-battery cutout has fired.
        static let stateCutout = danger

        /// The lit hearth: goguma is actively holding sleep off.
        ///
        /// One value, both appearances, every use: the menu bar bear, the
        /// popover bear, and the counter beside it. `brandFill` is single-valued
        /// for the same reason: the menu bar glyph sits on the user's wallpaper,
        /// where there is no app appearance to adapt to. Adaptive ember made the
        /// two bears resolve differently and drew the same animal in two colours
        /// a few points apart.
        ///
        /// Saturation follows the *ambient* colours, not the alerting ones.
        /// `accent` and `ok` are 45-51%; `warning` is 79% and earns it by being
        /// brief. A hold lasts as long as it lasts, so this sits at 45%.
        ///
        /// **Never used for temperature, and never inside the Safety section.**
        /// This app's central hazard is a laptop cooking in a bag, so warm
        /// already means "too hot" there. Ember is a fill and a numeral;
        /// `warning` and `danger` are always symbols with a shape of their own.
        ///
        /// The trade runs the other way from what you would expect. This value
        /// is tuned for the menu bar, where the bear sits on the wallpaper and
        /// wants to be bright: 7.9:1 on the dark app surface. On the *light*
        /// surface it is 2.0:1, which is well under the 4.5:1 body-text floor,
        /// so the hold counter is deliberately large (20pt) and is the only
        /// text this colour is ever allowed to carry.
        static let ember = adaptive(light: 0xD5A386, dark: 0xD5A386)

        // MARK: Sweet potato (the purple world, on trial)
        //
        // Saturation follows the same rule the rest of the palette does: the
        // ambient colours sit at 45-51%, so these do too. Pastel is achieved
        // with lightness, not by washing out the chroma, a light *and*
        // desaturated purple is the one that reads as a sticker rather than as
        // a considered choice.
        //
        // Kept beside the arctic values rather than replacing them, so trying
        // this costs one line in `surface`/`accent` and reverting costs the
        // same. See `Theme.world`.
        static let potatoSkin = adaptive(light: 0x8E5FA8, dark: 0xB98FD0)
        static let potatoFlesh = adaptive(light: 0xE8B84B, dark: 0xF0C766)
        static let purpleSurface = adaptive(light: 0xF4F0F8, dark: 0x18141D)
        static let purpleCard = adaptive(light: 0xFFFFFF, dark: 0x221C28)
        static let purpleDivider = adaptive(light: 0xE4DAEC, dark: 0x342C3C)
        static let purpleAccent = adaptive(light: 0x6D4A86, dark: 0xC0A0D4)
        static let purpleTextPrimary = adaptive(light: 0x241C2A, dark: 0xEFE9F3)
        static let purpleTextSecondary = adaptive(light: 0x6B6072, dark: 0x9C90A6)

        /// Alias, kept so the fill and text roles still read distinctly at the
        /// call site even though they now resolve to the same value.
        static let emberFill = ember

        /// A wake deliberately withheld.
        ///
        /// `warning`, deliberately **not** `danger`. "Held back, battery too
        /// low" is a safeguard working correctly. Styling it as a failure would
        /// teach users that the danger colour is noise, which is how a real
        /// failure ends up ignored. `wake_error` is the one that gets `danger`,
        /// and the two carry different symbols as well as different hues.
        static let stateSuppressed = warning

        /// A job goguma adopted automatically rather than one the user added.
        static let managed = textSecondary

        // MARK: The ice scene
        //
        // Lifted verbatim from the reference diorama, which is the brand world
        // made literal: a floe in calm water, a bear, penguins, and a seal
        // surfacing at its breathing hole on an irregular clock. The last of
        // those is the product drawn as a picture: wake, hold, sleep, wait.
        //
        // Its morning background is #EEF4F8, which is already `surface`. The
        // palette was arrived at independently and agreed, so the scene is not
        // decoration bolted onto the system; it is the same system.
        //
        // Morning maps to light appearance and dusk to dark, so the scene
        // follows the Mac rather than carrying a control of its own.

        enum Ice {
            // Dark mode is *moonlit*, not inverted.
            //
            // The first attempt made the floe near-black on the dark surface,
            // reasoning that dark mode darkens things. The result was animals
            // floating in a void with the floe's rim-light reading as a halo
            // ring around nothing, because ice is not dark at night, it is the
            // brightest thing in the scene. The values below dim and cool it
            // into a blue-grey and leave it clearly lighter than the ground, so
            // the white bear and the dark penguins both still read against it.

            /// Floe, lit top to shadowed underside.
            static let floeTop = adaptive(light: 0xF6FBFE, dark: 0x93A7B8)
            static let floeBottom = adaptive(light: 0xDCEAF3, dark: 0x6D8093)
            /// Open water around the floe.
            static let water = adaptive(light: 0xD8E7F1, dark: 0x2A3648)
            /// The dark of the aglu, where the seal comes up.
            static let waterDeep = adaptive(light: 0x42647B, dark: 0x101822)
            static let waterShallow = adaptive(light: 0x8AA9BE, dark: 0x2C4257)
            /// Hairline cracks in the ice.
            static let crack = adaptive(light: 0x6E93AC, dark: 0x4A5D70)

            static let bear = adaptive(light: 0xFCFEFF, dark: 0xF2F7FA)
            static let bearLine = adaptive(light: 0xD6E4EE, dark: 0xA8BDCC)
            /// The far-side legs. A shade of the bear, never the floe: tying it
            /// to the ice made the legs render as dark bars once the ice went
            /// moonlit.
            static let bearShade = adaptive(light: 0xE6EEF4, dark: 0xCEDCE6)
            static let bearEye = adaptive(light: 0x3C5160, dark: 0x2A3742)

            static let penguinBack = adaptive(light: 0x33454F, dark: 0x232F38)
            static let penguinFront = adaptive(light: 0xF4F8FB, dark: 0xEDF3F7)
            /// Beak and feet. The one warm note in an otherwise cold scene,
            /// and within a hair of the theme's `warning` amber, which is why
            /// the scene never looks like it came from a different product.
            static let beak = adaptive(light: 0xE2A150, dark: 0xC98F47)

            static let sealBody = adaptive(light: 0xA6BAC6, dark: 0x7A8B98)
            static let sealLight = adaptive(light: 0xCCD9E2, dark: 0x94A5B2)
            static let sealDark = adaptive(light: 0x93A8B6, dark: 0x64737F)
            static let sealEye = adaptive(light: 0x2E3F49, dark: 0x141B21)

            static let snow = adaptive(light: 0xFFFFFF, dark: 0xE3ECF3)
            /// Low sun on the horizon. Only drawn in dark ("dusk").
            static let duskGlow = adaptive(light: 0xE9C4AA, dark: 0xE9C4AA)
        }

        // MARK: Data visualisation

        /// The plotted series (run durations).
        static let chartSeries = accent
        /// A run whose outcome was not `ok`.
        static let chartSeriesAlert = danger
        /// The ceiling reference line.
        static let chartReference = warning
        /// Axis rules and gridlines.
        static let chartAxis = divider
        /// Area fill beneath the series.
        static let chartFill = accent.opacity(0.12)
        /// Hold time, against which the runtime series is compared.
        static let chartWaste = warning.opacity(0.55)

        // MARK: Resolution

        /// Builds a colour that resolves per appearance.
        ///
        /// A dynamic `NSColor` rather than a colour-scheme environment read:
        /// AppKit re-resolves it wherever it is drawn, including inside
        /// `NSHostingController` windows and the status item, so light/dark
        /// switching is live and no view needs a dependency on appearance.
        private static func adaptive(light: UInt32, dark: UInt32) -> Color {
            Color(nsColor: NSColor(name: nil) { appearance in
                appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                    ? srgb(dark)
                    : srgb(light)
            })
        }

        private static func srgb(_ hex: UInt32) -> NSColor {
            NSColor(
                srgbRed: Double((hex >> 16) & 0xFF) / 255,
                green: Double((hex >> 8) & 0xFF) / 255,
                blue: Double(hex & 0xFF) / 255,
                alpha: 1
            )
        }
    }

    // MARK: - Materials
    //
    // System materials rather than custom shadows: more Mac-native, and they age
    // better than a hand-rolled elevation scale.

    enum Materials {
        /// The one material in the app. `themeSurface()` lays the arctic
        /// surface colour over it; nothing else references a material directly.
        static let window: Material = .regularMaterial
    }

    // MARK: - Typography
    //
    // Scale is 11 / 13 / 15 / 20: caption, body, emphasised, popover title.
    // One family throughout: see the note inside `Typography`.

    enum Typography {

        // One family: the macOS system face, everywhere.
        //
        // This used to name Newsreader for display and Inter for body, falling
        // back to the system face when they were not installed, which they
        // never were, so every surface has always rendered in the system font
        // anyway. The aspiration was worse than useless: it meant installing
        // either font on any machine would silently change the whole app, and
        // it left three different notions of "the body face" in one file.
        //
        // The menu bar glyph, the popover and every window now read as one
        // program rather than as a design that half-arrived. Numbers that
        // change in place are still monospaced; that is a legibility rule, not
        // a family choice.

        enum Size {
            static let caption: CGFloat = 11
            static let body: CGFloat = 13
            static let emphasised: CGFloat = 15
            static let title: CGFloat = 20
        }

        /// The popover's one editorial moment, at 20pt.
        static let popoverTitle = Font.system(size: Size.title, weight: .semibold)

        /// Window and section headings.
        static let title = Font.system(size: Size.emphasised, weight: .semibold)
        /// The most important line on a surface.
        static let headline = Font.system(size: Size.emphasised, weight: .medium)
        /// Standard body copy.
        static let body = Font.system(size: Size.body, weight: .regular)
        /// Row labels.
        static let rowLabel = Font.system(size: Size.body, weight: .medium)
        /// Supporting text.
        static let caption = Font.system(size: Size.caption, weight: .regular)
        /// Quiet section labels.
        static let sectionLabel = Font.system(size: Size.caption, weight: .medium)
        /// A tracked small-caps group heading. See `GroupHeader`.
        static let groupHeading = Font.system(size: 10, weight: .semibold)
        static let groupHeadingTracking: CGFloat = 1.1
        /// The disclosure chevron beside a group heading.
        static let disclosureChevron = Font.system(size: 8, weight: .semibold)

        // Monospace is mandatory for anything whose digits change in place. A
        // proportional face makes a ticking counter jitter, which reads as
        // instability in a tool whose entire job is to be dependable.

        /// The live hold counter. Ticks once a second, so it must be mono.
        static let counter = Font.system(
            size: Size.emphasised, weight: .regular, design: .monospaced
        )
        /// Durations and numbers in tables.
        static let tabularSmall = Font.system(
            size: Size.caption, weight: .regular, design: .monospaced
        )
        /// A tabular figure that needs to stand out, a countdown about to
        /// fire. Same metrics as `tabularSmall` so the column stays aligned.
        static let tabularSmallEmphasised = Font.system(
            size: Size.caption, weight: .semibold, design: .monospaced
        )
        /// Commands, cron expressions, match patterns.
        static let code = Font.system(size: Size.caption, weight: .regular, design: .monospaced)

        /// Extra leading for body copy, targeting a ~1.4 line height.
        static let bodyLineSpacing: CGFloat = 3
        /// Headings run tight, around 1.15.
        static let headingLineSpacing: CGFloat = 0
        /// -0.018em at the title size.
        static let headingTracking: CGFloat = -0.36

        /// Roughly how far a capital letter rises above the baseline, as a
        /// fraction of the font size. Used to optically centre a glyph against
        /// text rather than baseline-align it, which parks it low.
        static let capHeightRatio: CGFloat = 0.36

        // Symbol sizes. SF Symbols render as text, so their size is a font.
        static let iconInline = Font.system(size: IconSize.inline)
        static let iconRow = Font.system(size: IconSize.row)
        static let iconHero = Font.system(size: IconSize.hero)

        /// The menu bar status item title. Computed, not stored: `NSFont` is
        /// not `Sendable`.
        static var statusItem: NSFont {
            NSFont.monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
        }
    }

    // MARK: - Spacing
    //
    // 8pt grid: 8 / 16 / 24 / 32 / 48. The two sub-grid values exist only for
    // inline chips and glyph gaps, where 8 would be larger than the element it
    // is spacing.
    //
    // Density target is "compact layout, airy within it": a tight outer rhythm
    // so the popover fits without scrolling in the common case, with generous
    // line spacing inside it.

    enum Space {
        /// Sub-grid. Gap between a glyph and its label, or inside a chip.
        static let xxs: CGFloat = 2
        /// Sub-grid. Chip padding.
        static let xs: CGFloat = 4

        static let sm: CGFloat = 8
        static let md: CGFloat = 16
        static let lg: CGFloat = 24
        static let xl: CGFloat = 32
        static let xxl: CGFloat = 48
    }

    // MARK: - Shape
    //
    // Two radii, scaled down from snowscroll's web values (11 / 18), which read
    // large in a 340pt popover.

    enum Radius {
        /// Large: panels and cards.
        static let card: CGFloat = 14
        /// Small: rows and controls.
        static let row: CGFloat = 8
        static let control: CGFloat = 8
        /// Sub-scale. At 8 an inline chip reads as a pill.
        static let badge: CGFloat = 4
    }

    enum Stroke {
        /// 1px hairline, always in the divider colour.
        static let hairline: CGFloat = 1
        static let series: CGFloat = 1.5
        static let sparkline: CGFloat = 1.2
        static let reference: CGFloat = 1
        static let referenceDash: [CGFloat] = [3, 3]
    }

    // MARK: - Icons
    //
    // The rule that outranks taste: **never colour-only**. Every state carries a
    // symbol alongside its tint, mirroring the CLI's `● ○ ✓ ! ✗`. `warning` and
    // `danger` in particular must never differ by hue alone: "hit its ceiling"
    // and "never ran" call for different actions, and a colourblind user, or
    // anyone looking at a grayscale screenshot, has to be able to tell them
    // apart.
    //
    // That is why `warning` is a triangle and `error` an octagon, rather than
    // the outlined-versus-filled pair of the same shape they used to be, which
    // was very nearly a colour-only distinction.

    enum Icon {

        // Status item. See `StatusItem` below for how these are rendered.
        //
        // snowscroll's mark is a minimal bear built from circles, and it maps
        // well here because a bear can sleep: awake with eyes open while
        // holding, asleep while idle, the same silhouette with a badge for
        // trouble. That asset does not exist yet, so these are the non-branded
        // stand-ins the brief names, chosen so the awake-versus-asleep read
        // survives pure monochrome at 16pt.
        static let holding = "eye"
        static let idle = "zzz"
        static let paused = "pause.circle"
        static let cutout = "exclamationmark.triangle.fill"
        static let disconnected = "bolt.horizontal.circle"
        /// A wake withheld on purpose. Reads as "staying asleep deliberately".
        static let wakeSuppressed = "zzz"

        // Severity. Three distinct silhouettes, legible without colour.
        /// `✓`
        static let ok = "checkmark.circle.fill"
        /// `!`
        static let warning = "exclamationmark.triangle.fill"
        /// `✗`
        static let error = "xmark.octagon.fill"
        static let info = "info.circle"

        // Actions
        static let skipNext = "forward.end"
        static let sleepNow = "moon.zzz"
        /// A manual hold. Deliberately not a coffee cup: this product's
        /// world is arctic, and the mug belongs to a different app.
        static let keepAwake = "sun.max"
        static let pause = "pause.fill"
        static let resume = "play.fill"
        static let jobs = "list.bullet"
        /// Rotated 90° when open, never swapped for a down-chevron.
        static let disclosure = "chevron.right"
        static let settings = "gearshape"
        static let history = "clock.arrow.circlepath"
        static let quit = "power"
        static let add = "plus"
        static let edit = "pencil"
        static let remove = "minus"
        static let test = "checkmark.circle"
        static let refresh = "arrow.clockwise"
        static let copy = "doc.on.doc"
        static let search = "magnifyingglass"
        static let clear = "xmark.circle.fill"
        static let sync = "arrow.triangle.2.circlepath"

        // Information
        static let nextWake = "alarm"
        static let clock = "clock"
        static let job = "briefcase"

        // Detection modes
        static let detectionMark = "target"
        static let detectionPattern = "text.magnifyingglass"
        /// Wake-only. A fixed window, hence a timer. Deliberately not a warning
        /// glyph: this is the most common mode and a correct configuration.
        static let detectionNone = "timer"
        /// Adopted automatically from a watched scheduler.
        static let managed = "sparkles"

        // Run outcomes
        static let outcomeOK = "checkmark"
        static let outcomeFailed = "xmark"
        static let outcomeCeiling = "arrow.up.to.line"
        static let outcomeNeverDetected = "questionmark"
        static let outcomeCutout = "bolt.slash"
        static let outcomeUnknown = "questionmark.circle"
    }

    enum IconSize {
        static let inline: CGFloat = 11
        static let row: CGFloat = 13
        static let hero: CGFloat = 20
    }

    // MARK: - Status item

    enum StatusItem {
        /// Renders the menu bar glyph as a template image, so AppKit tints it to
        /// match the menu bar in light, dark, and while highlighted.
        ///
        /// This is the Mac-native choice and what the brief asks for, and it has
        /// one consequence worth stating plainly: a template image **cannot
        /// carry colour**, so state in the menu bar is communicated by
        /// silhouette alone. That is exactly why the glyphs are as far apart as
        /// `eye` and `zzz` rather than two variants of one shape. Set this to
        /// false to tint by state instead; `StatusItemController` honours both
        /// paths.
        static let rendersAsTemplate = true

        /// Point size of the menu bar glyph.
        ///
        /// 18 rather than 16: the menu bar is 22pt tall, and with the mark
        /// cropped to its ink this fills the space the way system icons do
        /// without crowding the bar.
        static let glyphSize: CGFloat = 18

        /// The menu bar ink per state.
        ///
        /// White on a dark menu bar, deep arctic on a light one. Not a template
        /// tint; see `MenuBarMark.image(size:asleep:colour:)` for why the
        /// colour has to be painted into the image, so this is resolved
        /// against the button's own appearance and redrawn when that changes.
        ///
        /// Pure white only on the dark bar. White on a light menu bar is
        /// invisible, and an icon that disappears when someone switches
        /// appearance is a worse bug than the black one this replaced.
        static func tint(for state: GogumaState) -> NSColor {
            switch state {
            case .idle, .holding:
                NSColor(name: nil) { appearance in
                    appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                        ? .white
                        : NSColor(srgbRed: 0x2E / 255, green: 0x6A / 255, blue: 0x8F / 255, alpha: 1)
                }
            case .paused, .disconnected: NSColor(Colors.textSecondary)
            case .cutout: NSColor(Colors.danger)
            }
        }

        /// Bridges a theme colour into AppKit for the non-template path.
        ///
        /// The status item is an AppKit control, so it needs an `NSColor`
        /// rather than a SwiftUI `Color`. Doing the conversion here rather than
        /// at the call site keeps the rule literally true (no other file
        /// mentions a colour type at all), so the audit that enforces it stays
        /// a simple search rather than one with exceptions.
        static func tint(_ color: Color) -> NSColor { NSColor(color) }
    }

    // MARK: - Surface geometry

    enum Surface {
        /// How strongly the arctic surface colour is laid over the system
        /// material.
        ///
        /// A `Material` on its own is neutral grey: it samples the desktop and
        /// desaturates it, which is why an app built entirely on materials looks
        /// like every other Mac app regardless of its palette. snowscroll's
        /// surfaces are a cool near-white and a deep slate, and at 0 opacity
        /// neither of them was reaching the screen; the palette existed in the
        /// theme file and nowhere else.
        ///
        /// Not 1.0: at full opacity the vibrancy goes too, and a popover that
        /// does not pick up any of what is behind it stops reading as a popover.
        /// This keeps the translucency and puts the colour back.
        static let tint: Double = 0.82

        /// Height given to the ice scene in an empty state.
        ///
        /// The artwork is drawn 360×250 and scales to fit, so this is the one
        /// number that decides how large it reads. Deliberately under half the
        /// height of the smallest window that shows it: it is the reason the
        /// pane is not blank, not the reason the pane exists.
        static let sceneHeight: CGFloat = 200

        static let popoverWidth: CGFloat = 340
        static let popoverMaxHeight: CGFloat = 620
        /// One cell of the popover's 2×2 control grid: the width left after the
        /// horizontal padding, halved, less the gap between the columns.
        static var popoverControlCellWidth: CGFloat {
            (popoverWidth - Space.md * 2 - Space.xs) / 2
        }
        /// Cap on the scrolling middle, leaving room for the pinned header and
        /// footer within `popoverMaxHeight`.
        static let popoverMaxScrollHeight: CGFloat = 470
        /// Height of the compact "daemon isn't running" panel in the popover.
        /// No longer caps a job list; that list no longer scrolls on its own.
        static let popoverOfflinePanelHeight: CGFloat = 150

        /// Sized to fit a normal machine's job list without slack.
        ///
        /// 560 left about 120pt of empty list below the last row at nine jobs,
        /// which is most of a screenful of nothing in the part of the window
        /// that is supposed to be the content. The window is resizable, so
        /// someone with thirty jobs drags it once; starting snug is the better
        /// default because it is the one nobody has to correct.
        /// Sized to the list, not to the list plus a detail pane.
        ///
        /// The empty-state band under the list is gone, so 500pt left ~90pt of
        /// blank surface below the last job whenever nothing was selected,
        /// which is most of the time. The window is resizable and the list
        /// scrolls, so a shorter default costs nothing when there are more
        /// jobs, and selecting one grows the content rather than revealing
        /// space that was always there.
        static let jobsWindowSize = CGSize(width: 900, height: 472)
        static let jobsWindowMinSize = CGSize(width: 720, height: 320)
        static let jobsDetailPaneHeight: CGFloat = 176

        /// Wide enough for every column at its ideal width.
        ///
        /// The seven columns want 700pt between them, and a `Table` adds its
        /// own insets and a separator per column on top, so at 760 the last
        /// column ("Woke Mac") was clipped mid-header, which reads as a broken
        /// table rather than a narrow one. The minimum stays where the columns
        /// can still reach their stated minimums (590pt of content).
        static let historyWindowSize = CGSize(width: 840, height: 620)
        static let historyWindowMinSize = CGSize(width: 620, height: 460)

        /// Fits the settings exactly. If a new setting stops it fitting, that
        /// is a signal there are too many, not a reason to add a scrollbar.
        /// Width only. The settings pane measures its own height and sizes its
        /// window to it (see `FitsWindowHeight`) because the height that fits
        /// **Advanced** open is ~100pt taller than the height that fits it
        /// closed, and closed is how it always opens.
        static let settingsWidth: CGFloat = 520
        /// The height the window opens at, before the content reports its own.
        static let settingsWindowSize = CGSize(width: 520, height: 520)

        static let editSheetWidth: CGFloat = 480
        static let editSheetMatchResultsHeight: CGFloat = 110
    }

    // MARK: - Chart geometry

    enum Chart {
        /// Ceiling on hold-bar width, so few runs do not become slabs.
        static let maxBarWidth: CGFloat = 26
        static let sparklineSize = CGSize(width: 72, height: 18)
        static let sparklineDotRadius: CGFloat = 1.8
        static let historyHeight: CGFloat = 200
        static let insetY: CGFloat = 4
        static let insetX: CGFloat = 2
        static let dotRadius: CGFloat = 3
        static let axisLabelWidth: CGFloat = 52
    }

    // MARK: - Motion

    enum Motion {
        static let duration: TimeInterval = 0.36
        static let standard = Animation.timingCurve(0.4, 0, 0.2, 1, duration: duration)
    }

    /// The standard animation, or none when the user has asked for reduced
    /// motion.
    ///
    /// Every animated call site routes through this. Two things are never
    /// animated regardless of the setting: the live mono counter, which must
    /// snap rather than ease or the digits smear as they tick, and the menu bar
    /// glyph, where legibility beats polish.
    static func motion(reduced: Bool) -> Animation? {
        reduced ? nil : Motion.standard
    }

    // MARK: - Timing (behaviour, not appearance)

    enum Timing {
        /// Poll cadence while a popover or window is on screen.
        static let activePollInterval: Duration = .seconds(1)
        /// Cadence when only the menu bar item is visible.
        static let idlePollInterval: Duration = .seconds(30)
        /// Socket deadline for one request.
        static let socketTimeout: TimeInterval = 5
    }

    // MARK: - Shapes

    static var cardShape: RoundedRectangle {
        RoundedRectangle(cornerRadius: Radius.card, style: .continuous)
    }

    static var rowShape: RoundedRectangle {
        RoundedRectangle(cornerRadius: Radius.row, style: .continuous)
    }

    static var badgeShape: RoundedRectangle {
        RoundedRectangle(cornerRadius: Radius.badge, style: .continuous)
    }
}

// MARK: - Hairline

/// A 1px rule in the divider colour.
///
/// Sections are separated by these and by space, never by boxes. The editorial
/// hairline is the most recognisable part of the system, and boxing every group
/// would lose it while also costing vertical space the popover does not have.
struct ThemeHairline: View {
    var body: some View {
        Rectangle()
            .fill(Theme.Colors.divider)
            .frame(height: Theme.Stroke.hairline)
            .accessibilityHidden(true)
    }
}

// MARK: - Token-backed view modifiers
//
// Call sites express intent ("this is a section") rather than geometry ("16pt
// padding, 14pt radius"), so the next redesign can change what a section looks
// like without editing a view.

extension View {
    /// The app background: system material with the arctic surface laid over it.
    ///
    /// Every window and the popover use this rather than a bare `Material`. See
    /// `Theme.Surface.tint` for why the material alone was not enough.
    func themeSurface() -> some View {
        background {
            ZStack {
                Rectangle().fill(Theme.Materials.window)
                Rectangle().fill(Theme.Colors.surface).opacity(Theme.Surface.tint)
            }
            .ignoresSafeArea()
        }
    }

    /// A raised panel, for the few places a real surface is still wanted.
    ///
    /// The hairline border is what makes a card read as arctic rather than as a
    /// plain white rectangle: card and surface are close in value by design, so
    /// without an edge the card has almost no shape against the background.
    func themeCard() -> some View {
        padding(Theme.Space.md)
            .background(Theme.Colors.cardFill, in: Theme.cardShape)
            .overlay {
                Theme.cardShape
                    .strokeBorder(Theme.Colors.divider, lineWidth: Theme.Stroke.hairline)
            }
    }

    /// A compact row inside a list.
    func themeRow() -> some View {
        padding(.vertical, Theme.Space.xs)
            .padding(.horizontal, Theme.Space.sm)
    }

    /// A small inline chip: detection mode, outcome, source.
    func themeBadge(_ tint: Color) -> some View {
        font(Theme.Typography.caption)
            .foregroundStyle(tint)
            .padding(.horizontal, Theme.Space.xs)
            .padding(.vertical, Theme.Space.xxs)
            .background(tint.opacity(0.12), in: Theme.badgeShape)
    }

    /// Multi-line prose, at the body line height.
    func themeProse() -> some View {
        lineSpacing(Theme.Typography.bodyLineSpacing)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// A heading: tightly tracked, tight leading.
    func themeHeading() -> some View {
        tracking(Theme.Typography.headingTracking)
            .lineSpacing(Theme.Typography.headingLineSpacing)
    }

}
