# The goguma landing world

One page, `index.html`, no build step, no dependencies, no network requests. The
whole thing is a single file plus `assets/` — 530 KB in total. That is
deliberate: it can be served from GitHub Pages, opened with `open
site/index.html`, or pasted anywhere.

Images are **WebP**, not PNG. These are 2x composites with film grain over a
gradient, which is close to the worst case for PNG — the grain defeats its
predictors and every scene came out near 2 MB, for a page total of 6.2 MB. The
same frames at WebP quality 90 are 60–95 KB each.

## Thesis

A sleeping laptop silently eats your scheduled jobs. The page proves goguma
by showing the real menu bar app doing the real thing, and refuses the developer
tool default — a terminal on a dark gradient, three feature bullets, a wall of
badges.

## The world

**Ground is light, but not white.** `#ECE5F3`, a pastel purple that reads as
paper rather than as a tinted white. It started two steps paler at `#F4F0F8` and
everything below the hero read as plain white paper — the sky established a world
and the page abandoned it a screen later. Ink is `#241A2C`, a deep aubergine,
never pure black. Secondary text is `#6B6072`; the hairline rule is `#DED2EA`.

`GROUND` in `build-sky.py` must equal `--ground` in the stylesheet. Every sky
ramp terminates on it exactly, and that is the only reason the hero and the
closing section dissolve into the page instead of cutting against it. Change one
and you must change the other, then re-run `python3 build-sky.py`.

**One accent, used sparingly.** `#6D4A86` for buttons, links and the gradient in
the big number. It is the same purple family as the ground, so nothing shouts.

**One ember, used almost never.** `#B4531A` appears in exactly two places: the
hold counter inside the real product screenshots, and the measured durations in
the ledger. It means *the product is doing something right now*. Spending it
anywhere else would make it stop meaning that.

**Shadows are neutral aubergine-black**, `rgba(36,26,44,…)`, and their depth comes
from offset and blur rather than from colour. Tinting a shadow with its object's
own hue makes the object read as *lit* rather than *lifted* — a different and
usually unintended claim. The hero button had drifted to `rgba(40,22,64)`, a
noticeably more saturated purple, and was brought back.

**Dark appears twice, and both times it is a thing you use.** The hero scene's
desktop, and the install block. Nothing else on the page is dark, so both read
as lit from within and the eye goes to them without being told to. The plate and
the Jobs window sit on the page's own light ground instead — a light panel on
light paper, held up by a contact shadow rather than by a dark backdrop.

## Type

Apple keynote scale. `h1` is `clamp(44px,7.4vw,88px)` at `-.035em` tracking and
`1.04` line height — enormous and tight. `h2` runs to 62px. Ledes cap at `37ch`
and use `text-wrap:balance`; at the original `31ch` they set as four short lines
with a stub on the end, which reads as an accident under type that size.

Eyebrows and all numeric data are monospace at 11–13px with wide tracking. That
is the only voice change on the page, and it separates *measurement* from
*claim*.

No cards. No borders. No icon tiles. No drop shadows on anything that is not a
screenshot. Sections are separated by whitespace and hairline rules at
`#E4DAEC`, nothing else.

## Rhythm

Five stacked centred acts read as a template no matter how good each one is, so
one act — *It waits, then it wakes* — runs as a two-column split with the copy
left and the plate right. It also fixes a scale problem: a 400px plate alone in
a 1180px column looks lost, while the same plate beside a column of text reads
as the deliberately small thing it actually is.

## What the page argues

One claim, in this order: your Mac sleeps and cron does not know that → here is
what goguma actually does about it → here is it finding your jobs → here are
the three lines that install it.

Five blocks. It used to be eight, and the extra three were the problem: a
statistic about how much the machine slept, a ledger of what jobs "used to be
held for", and a stack of quiet facts. All three were about the mechanism rather
than about the visitor, and "held for" was an internal notion that never
appeared in the product at all. They are gone.

The copy also stopped saying "your Mac" outside the headline. The page does not
know anything about the reader's machine; it shows one real schedule and lets
them recognise it.

### The dial

A 24-hour clock. A sweep travels the face; as it reaches each scheduled job the
little machine at the centre powers on, runs it, and powers off again, and
between jobs it counts down to the next one.

This is the fourth attempt at the same claim and the first that shows the *shape
of the day*. A strip with ember ticks and then a pulse line were accurate
diagrams that did not look like anything happening to a computer. A close-up of
the desktop dimming and lifting was literal, which was better, but it shows one
moment at a time — so "awake for the job, asleep for the rest" was something you
had to assemble from a loop rather than something you could see at a glance. A
dial holds the whole day and the machine's state within it simultaneously, which
is the actual argument.

The hours are real: the four jobs sit at 06, 09, 12 and 18 because that is when
they run, evenly spaced so their labels never collide. Two of the durations are
the app's own measurements; the other two are stated as ceilings (`up to 3m`)
because those jobs have no measured duration yet. The total says **at most** 45m
awake for the same reason — it is the sum of the caps, not an observation, and
saying so is the difference between a figure and a claim.

**The rotating sweep is its own SVG, stacked over the dial.** An SVG transform is
not composited: rotating a `<g>` inside the dial repainted the entire 640x400
drawing sixty times a second, permanently — so every per-job animation then
landed on a compositor that was already saturated, and the page hitched on every
cron beat. On its own layer with `will-change:transform` it rotates for free, and
the dial underneath repaints only when a job actually changes state.

**A full-bleed background must out-resolve the largest display it will cover.**
The sky was 2560px wide, which is an *upscale* on any retina screen — 0.89x at a
1440 viewport, 0.67x at 1920. That is the softness people kept reporting as
"blurry", and no amount of compression quality fixes an image smaller than the
surface it is stretched over. It is 3840x2400 now, and 79KB, because a soft cloud
field compresses almost for free.

**`backdrop-filter` is not free.** The Dock carried `blur(20px) saturate(1.5)`,
which makes the browser re-blur that region continuously — directly under the
animating cursor and panel. A flat translucent fill is visually indistinguishable
over a soft wallpaper and costs nothing. There is now no `backdrop-filter`
anywhere on the page.

**`color-mix(…, transparent)` painted nothing here.** The scan beam used
`color-mix(in srgb, var(--accent) 11%, transparent)`; the computed value looked
correct and the element rendered no pixels. An explicit `rgba()` works. Worth
knowing before reaching for `color-mix` in anything load-bearing.

**Measure the thing, not a remembered coordinate.** Three rounds of "the beam is
invisible" were partly my own error: I took the element's position from one
browser run and sampled pixels from a *different* one, and the section sat ~105px
apart between them. Scanning the whole image for the effect — rather than a band
I had computed earlier — found it immediately.

**Moving an element out of a container invalidates every selector scoped to it.**
The sweep was lifted from inside the dial SVG into its own `.sweeplayer` layer so
its rotation would stop repainting the whole drawing. Its animation rule stayed
`.dial .sweep`, which no longer matched anything — so the performance fix
silently stopped the hand turning altogether. Nothing errors when a selector
matches nothing; the element simply sits there looking deliberate.

Worth a standing check after any structural move: assert that every element you
expect to animate reports a non-`none` `animationName`. That test finds this
class of bug in one pass, where reading the CSS does not.

**The hand lingers on each job rather than sweeping past it.** `wg-round` is no
longer `to{rotate(360deg)}` — it keyframes the arrival percentage of each job's
own hour, then advances only 7 degrees across the 7% of the cycle that job is
awake. About 20 deg/s at a job against 90-140 between them, so the machine
flipping to AWAKE has time to register. The arrival percentages are derived from
the same `h/24` the delays use, so hand and job cannot drift apart.

**Measure a resting gap with animations disabled.** Every probe of the space
between the menu bar and the panel read 1px and every one was wrong: they sampled
the panel mid-`drop`, where a `translateY(-14px)` cancels most of the offset. The
gap you actually see was 15px. Read layout under `--force-prefers-reduced-motion`,
or from a single screenshot's pixels — never from a live animated frame.

**Duty cycle is the thing, not speed.** The scan beam was on the same 17s loop as
the rows and swept during 6%–22% of it: 2.7 seconds of motion, then 14.3 seconds
parked at zero opacity. Four times out of five you looked at the section and it
was doing nothing — so it read as broken, and speeding it up did not help because
the problem was never speed. It now runs its own 3.4s loop, independent of the
row cycle, so it is scanning whenever anyone looks at it.

**Animating slowly is not the same as animating.** The scan beam crawled 74px
over 3.9 seconds — about 19px a second, with ease-in-out on both ends, so it
stalled at each stop. It was running the entire time and read as frozen. One fast
linear pass at ~200px/s reads as a scan.

**Bitmaps must land on whole pixels.** The hero panel was positioned with
`right:clamp(14px,4.6vw,86px)`, which puts its left edge on a fractional x at
most viewport widths, and `height:auto` derived 219.375px from its aspect ratio.
A bitmap on a fractional origin *or* with a fractional height is resampled at a
subpixel offset and looks soft — which is why raising the capture from 2x to 3x
to 4x and going lossless never fixed it. Fixed `right` and explicit integer
`width`/`height` did. The 0.18% aspect deviation from rounding is invisible; the
resampling was not.

**Never animate an SVG geometry attribute.** `wg-ring` originally animated `r`
from 4 to 11. `r` is geometry, not paint: changing it invalidates the SVG's
geometry and repaints the entire dial every frame — and it fires exactly when a
job hits, so the page stuttered on every single cron beat. It is a `transform:
scale()` now, which the compositor handles for free.

**One `.hide` group, not four nested ones.** The source ANDs four "is any job
awake" states by nesting four opacity-animated groups around the 24 countdown
texts. Nested opacity forces the whole subtree to re-composite every frame. A
single group with four hidden windows in one keyframe does the same job; the
window boundaries are `h/24×100 + 0.41%` to `+7.40%`, the exact complement of
each job's AWAKE state.

**Scope the delay rules to match the animation rules.** They are written
`.dial .dN{animation-delay:…}`, not `.dN{…}`, and that is not cosmetic: the
`animation` shorthand resets `animation-delay` to 0, so a bare `.dN` at (0,1,0)
loses to `.dial .scr{animation:…}` at (0,2,0). Every element ran at delay 0, the
four job cycles fired in lockstep, and the machine lit four times at once instead
of once as the sweep reached each job — the entire point of the piece, gone. The
source file worked because both its selectors were single-class and the delay
came later in source order; prefixing one and not the other broke it.

Every element runs one 8s loop offset by `animation-delay`; nothing coordinates
anything at runtime, and a node's delay is just its own hour, `h/24 × 8 − 8`.
Hours hand off with no shared frame — `wg-hour` ends at 4.1656% and the next
begins at 4.1666% — because cross-fading two different strings in one position
overlays them rather than replacing them. The same trick is why `AWAKE` and the
countdown are exact complements switched instantly instead of dissolved.

### The search

The claim is that it finds what is already on the machine, so the section shows a
cursor going through one: it clicks each place a schedule can hide, the result
lands beside it, and at the one that actually has something the nine jobs tick in
underneath.

Every path and every job name is real. The choreography is not — this is a
reenactment, and the caption says so.

**Two earlier versions, and why each failed.** First a hand-built panel of rows.
Static, and worse, *wrong*: I had typed the results in by hand and given launchd
zero, when launchd really turns up 38 entries here. Then a VHS recording of the
real command (`docs/media/import.tape`), which is honest and cannot drift from
the product — and is also just a terminal, which is not what the section needed.
The recording is kept for the README, where a terminal is exactly right.

The resting state is the finished state: every row visible, every job found, the
cursor hidden. Animation supplies only the journey to it, on `backwards` fill, so
with animation off the section still says precisely what it means.

### Nothing about visibility may depend on JavaScript

This is the rule the page kept violating, in three different places, and it is
worth stating plainly.

`.rise{opacity:0}` with an IntersectionObserver adding `.in` means every section
is invisible unless a script runs. One thrown error above that line and the
visitor gets a headline and then a blank page. It is the same defect the hero was
fixed for and it was left in place everywhere else.

The reveal is now `animation-timeline: view()` inside `@supports` — driven by
scroll position, no script involved, and where it is unsupported the block simply
never applies and the content is plain visible. There is no state in which the
page is blank.

The corollary for review: **headless Chrome does not advance the animation clock
under `--virtual-time-budget`.** Anything mid-entrance photographs as missing.
`--force-prefers-reduced-motion` renders the settled state; use it, or you will
spend three rounds diagnosing artifacts.

## Structure## Structure## Structure

Onlook's shape, which the user pinned as the reference: stacked full-bleed
product acts, each one a single claim over one large real screenshot, closing on
a two-column FAQ.

1. **Hero** — see below. The page opens as a Mac.
2. **The night** — the full-bleed dark sky, 63% counting up, and the nine real
   jobs that died in it.
3. **It waits, then it wakes** — the popover with its job list open.
4. **It learns what your jobs actually cost** — the measured-durations ledger.
   This is the strongest evidence on the page: a three-minute hold that turned
   out to be seventeen seconds. It is set large enough to be the thing you look
   at — struck-through guess, arrow, measured figure at 24px in the ember — not
   as the 13px footnote it started as.
5. **It finds your jobs** — the Jobs window, plus three quiet facts.
6. **Three lines, and it's watching** — the install block.
7. **FAQ**, then **Let it sleep.** on dusk.

The install block is the actual call to action — everything above it is argument
— so it is set at reading size with a Copy button, rather than as a caption.

## The page is a Mac

There is no website navigation. goguma is a menu bar app, so the first screen
is a desktop: a real macOS menu bar across the top with the app as the frontmost
application, a wallpaper, the app's own panel hanging off its status item, and a
Dock. The reference is onlook.cam, which is a menu bar app selling itself the
same way.

**This is the answer to two separate problems at once.**

The nav was the first. A site nav *and* a fake menu bar had been tried together
and looked broken — two stacked bars read as chrome someone forgot to remove.
Adding gap between the links never fixed it because the problem was that there
were two bars. Committing to one, and making it the machine's rather than the
website's, does.

The hero image was the second. It had been a framed screenshot floating in the
middle of the page, and it read as low quality even though it was rendered at 3x
and its text was genuinely crisp. Three things caused that: two thirds of the
frame was empty wallpaper, the wallpaper was a soft blur sitting next to
razor-sharp UI, and a plain rounded rectangle with a white ring reads as a
picture placed on a page rather than as a screen.

None of those can happen now, because there is no screenshot. The panel is a
real render with an alpha channel, positioned under the status item it actually
belongs to. There is no frame around it and no picture of a Mac — the page is
the Mac.

- **The menu bar** carries genuine SF Symbols from `build-symbols.swift`, and
  goguma's glyph keeps its colour among the monochrome, which is the one
  thing that makes it findable on a real menu bar.
- **It scrolls away with the page.** A macOS menu bar that followed you down is
  the single detail that would give the whole thing away.
- **The wallpaper is the pastel cloud sky** (`sky.webp`), the same one the page
  is built around. A richer, darker wallpaper was built for it and rejected —
  more saturated is not more convincing, and it pulled away from every other
  surface on the page.
- **The menu bar is a translucent darkening, and the wallpaper belongs to
  `.mac`.** Those are one fix, not two. The bar is a *sibling* of the header, so
  with the wallpaper set on the header the bar had nothing behind it but the
  page's flat ground — every veil tried on it read as a solid stripe because
  there was nothing to see through. Moving the wallpaper up to the wrapper fixed
  the cause; darkening rather than lightening fixed the rest, since tinting
  toward the ground hides the very variation that makes a bar look translucent.
  No `backdrop-filter` either: blurring a pale wallpaper flattens it to grey.
  Verified by sampling — the bar is darker than the wallpaper directly below it
  at every x, which is how the reference behaves.
- **A cursor reaches up and clicks the status item, and the panel drops out of
  it.** That ordering carries the argument: goguma is reached by clicking a
  menu bar icon, so showing the panel already open skips the one gesture that
  explains where the app lives.

  It leaves the moment the click lands rather than drifting away afterwards —
  loitering on the wallpaper with nothing left to do reads as a stray graphic
  rather than as someone having just opened the app.

  The pointer lives in the `.mac` wrapper around *both* the bar and the desktop,
  not inside `header` — the header begins below the bar and clips its overflow,
  so a cursor positioned inside it could never reach the thing it clicks. Its
  rest position is verified to land inside the status item at 1280, 1440 and
  1920, and the click ring is centred on it. It is a black arrow with a white
  outline, which is the macOS cursor; white-on-black is Windows.

- **The panel is captured at 4x and stored losslessly.** At 340 CSS px it is 680
  device pixels on a 2x display, so a 1360px source is a clean 2:1 downscale —
  where 3x was 1020 into 680, a 0.667 resample that softens 11px type however
  good the filter. And lossy WebP softens small text at any quality: 96 was still
  visibly worse than the source. It is the one image on the page a visitor reads
  *text* out of, so it is worth 92KB. The photographic composites stay lossy.

  `S` in `build-scenes.py` must equal `SurfaceRenderer.captureScale`. They were 3
  and 4 for one build and the composited menu bar came out three quarters of its
  correct size against the popover. Frame dimensions there are derived from the
  popover's actual width for the same reason — a hardcoded `1020` silently gave
  the close-up the wrong aspect the moment the capture scale changed.

- **The cursor is the real bitmap** from the Windows 11 Cursors Concept pack
  (tailless, dark set), converted from `arrow.cur`. A hand-drawn path is never
  quite the shape people know, and mine was both too large and too heavy.

- **The panel's entrance translates but never scales, and its shadow is
  `box-shadow` on the image rather than `filter: drop-shadow` on the animating
  container.** Either one puts the panel on a composited layer that Chrome
  rasterises once and resamples, which turns 11px UI text to mush. Measured with
  edge energy in the panel: 157k with the animation off, 8k with it on. After the
  fix, 157k in both — verified by pinning the animation with a negative
  `animation-delay`, which jumps to a mid-animation frame at style time and so
  works even though headless freezes the animation clock.
- **The Dock** is what finishes it. A menu bar alone reads as a stripe; a menu
  bar with a Dock reads as a desktop.
- **The copy is bottom-aligned, not centred.** `justify-content:center` splits
  the leftover height evenly above and below, which put the copy high against the
  menu bar *and* left a pool of empty wallpaper between it and the Dock. Two
  complaints, one rule. Sending all the slack to the top fixes both.

### Nothing about visibility may depend on JavaScript

This is the rule the page kept violating, in three different places, and it is
worth stating plainly.

`.rise{opacity:0}` with an IntersectionObserver adding `.in` means every section
is invisible unless a script runs. One thrown error above that line and the
visitor gets a headline and then a blank page. It is the same defect the hero was
fixed for and it was left in place everywhere else.

The reveal is now `animation-timeline: view()` inside `@supports` — driven by
scroll position, no script involved, and where it is unsupported the block simply
never applies and the content is plain visible. There is no state in which the
page is blank.

The corollary for review: **headless Chrome does not advance the animation clock
under `--virtual-time-budget`.** Anything mid-entrance photographs as missing.
`--force-prefers-reduced-motion` renders the settled state; use it, or you will
spend three rounds diagnosing artifacts.

## Structure## Structure## Structure

Onlook's shape, which the user pinned as the reference: stacked full-bleed
product acts, each one a single claim over one large real screenshot, closing on
a two-column FAQ.

1. **Hero** — see below. The page opens as a Mac.
2. **The night** — the full-bleed dark sky, 63% counting up, and the nine real
   jobs that died in it.
3. **It waits, then it wakes** — the popover with its job list open.
4. **It learns what your jobs actually cost** — the measured-durations ledger.
   This is the strongest evidence on the page: a three-minute hold that turned
   out to be seventeen seconds. It is set large enough to be the thing you look
   at — struck-through guess, arrow, measured figure at 24px in the ember — not
   as the 13px footnote it started as.
5. **It finds your jobs** — the Jobs window, plus three quiet facts.
6. **Three lines, and it's watching** — the install block.
7. **FAQ**, then **Let it sleep.** on dusk.

The install block is the actual call to action — everything above it is argument
— so it is set at reading size with a Copy button, rather than as a caption.

## The hero

Flat near-black, left-aligned, and the product shot is the biggest thing on the
screen.

### What the references actually do

Three attempts here failed before I went and read the real ones. Raycast, Linear
and Warp are the closest comparables — two Mac developer tools and one
productivity launcher — and they agree with each other against everything I had
built:

- **Not one of them has a decorative gradient.** Raycast is flat black. Linear
  is flat black. Warp is flat white. No blobs, no mesh, no aurora.
- **Linear and Warp left-align the headline** on the page margin. Only Raycast
  centres, and it does so on pure black with nothing else in the viewport.
- **The sub-headline is small** — 16–17px, one or two lines.
- **The product screenshot is enormous, sharp, and immediately below the copy**,
  bleeding off the bottom of the viewport. You can read individual rows of it.

### What was here before, and why it was slop

First a full-bleed purple-to-orange gradient wash. Then, when that was called
out, two "tasteful" low-opacity violet fields drifting behind the type — which
is the same instinct, just quieter. The fix was never a subtler gradient. It was
none. A dark rectangle with nothing on it reads as confidence; a dark rectangle
with clouds on it reads as filler.

The other half of it was scale. The hero screenshot was a 300px strip with the
popover cropped through a button row, and separately the popover was rendering
at double its real size because a 2x asset was being shown at 1:1 CSS pixels.
The scenes are now composited on a 2400px-wide desktop, which puts the popover
at ~28% of the frame — back near its true 340pt, and legible.

### The current hero

- **Flat `#08060C`.** No blooms, no grain, no gradient.
- **Left-aligned** in the same 1180px container as every heading below it, so
  the headline starts on the same line as the rest of the page.
- **80px headline, 18px sub, mono spec line.** A solid button next to an
  underlined text link rather than two buttons — two buttons make the visitor
  choose, a link makes the choice obvious.
- **One word in an italic serif** — *sleeps* — the only non-system face on the
  page, on the verb the whole product is about.
- **A real Mac screen** at container width, running off the bottom, cross-fading
  idle to holding. Phones get a differently *composed* narrow cut via
  `<picture>`, not a differently cropped one: `object-fit:cover` on the wide
  frame in a portrait box threw the popover off the side of the screen.

### Two layout traps worth remembering

**`margin:0 auto` on a flex item turns off `align-self:stretch`.** The wrapper
then shrink-wraps, and a `width:100%` child resolves against a parent whose width
depends on that child. It computed to zero and the entire screenshot vanished.
`header > .wrap{width:100%}` fixes it.

**Never `margin-top:auto` to push something to the bottom of a hero.** It
collapses to zero the moment content exceeds the container, and the thing you
pushed lands on top of the line above it. Use a real gap and let a `flex:1`
sibling absorb the slack.

### The entrance must not be load-bearing

The stagger is pure CSS with no JavaScript, and the hidden state lives in the
keyframes with `animation-fill-mode: backwards` — never as `opacity:0` on the
element itself with a `both` fill.

Both spellings look identical when everything works. They differ completely when
something doesn't: with the hidden state on the element, the headline exists only
for as long as an animation is holding it visible, so a blocked script or a
stalled animation leaves an empty rectangle where the argument should be. With
`backwards`, the element's resting state is the visible one and the animation
only borrows the hidden state on the way in.

This was wrong twice — first gated behind a JS `.loaded` class, then still
resting at `opacity:0` — and both times the hero rendered blank under review.

### Reviewing a page whose hero animates

Headless Chrome does not advance the animation clock under `--virtual-time-budget`,
so every screenshot of the hero came back with no headline and I kept explaining
it away. `--force-prefers-reduced-motion` renders the settled state. Use it for
every review screenshot.

## The screenshots are real

Every product image is rendered from the running app by `SurfaceRenderer`,
against the author's live background service and the author's nine real cron
jobs. Nothing is a mockup and nothing is a flattering fixture — the job names,
the schedules, the durations and the 63% are all as measured.

`build-scenes.py` composites them, and produces three kinds of image on purpose:

- **scene** — the popover on a desktop under a menu bar. An establishing shot:
  *where does this live.*
- **plate** — the panel alone, corners rounded, alpha preserved, dropped onto
  the page's own ground. A detail shot: *what does it say.*
- **window** — a document window with the title bar and traffic lights that
  `SurfaceRenderer` cannot capture, since it photographs the content view only.

The page alternates them. Two scenes back to back repeat the same wallpaper and
the same menu bar and start to read as a template rather than as two moments.

### Getting the fake desktop to stop looking fake

Everything below was a defect found by looking at the composite at full size,
and every one of them is the kind a Mac user clocks in half a second without
being able to say why:

- **The menu bar glyphs were drawn with arcs and rectangles.** They are now
  genuine SF Symbols, rendered to PNG by `build-symbols.swift`. Hand-drawn wifi
  and battery icons never match at any size.
- **The type was the wrong face.** `SFNS.ttf` is a variable font whose axes are
  Width, Optical Size, GRAD and Weight; asking for `index=1` expecting bold
  returns a named instance from a list hundreds long, and the app name came out
  stamped on rather than typeset. The axes are now set explicitly, Optical Size
  included — San Francisco has genuinely different letterforms for text and
  display sizes, and leaving that at its 28 default while drawing at 13.5pt
  gives a headline face's spacing at body size.
- **The popover had square corners.** macOS rounds its panels at 12pt. A
  square-cornered render pasted onto a desktop is the single most obvious tell.
- **The wallpaper was two blurred ellipses**, which has no colour structure —
  only mud. It is now an inverse-distance mesh gradient across nine anchors,
  with a faint diagonal sheen and grain at the threshold of visibility.
- **The menus overlapped the status items.** The bar now lays out right to left
  and drops any menu that would come within 34pt of them, the way macOS does.
- **The traffic lights were half size.** 12pt across on 20pt centres.
- **The plate's edge dissolved into the page**, because the panel's background
  is within a few points of the page ground. It needed a tight contact shadow,
  not more ambient blur.

Scenes are sized from the popover rather than from a fixed canvas. An earlier
pass used a fixed 1180px canvas and silently clipped the 1324px jobs popover off
the right edge; deriving the canvas from the content makes that impossible.

The two hero states are **generated** at a common height rather than padded
afterwards. Padding by stretching a two-pixel tail smeared the wallpaper's grain
into vertical streaks along the bottom of whichever frame was shorter.

## Motion

Three effects, all of which degrade to a fully readable static page:

- **Hero cross-fade.** A 9s two-image cycle, pure CSS, paused by an
  `IntersectionObserver` when the stage is off screen so a backgrounded tab is
  not compositing full-bleed images forever.
- **Rise on entry.** Elements fade up 14px once. The observer's bottom
  `rootMargin` is in **pixels, not percent** — a percentage resolves against the
  viewport, and on a tall window it carved out a dead band deep enough that the
  closing headline never intersected and never appeared.

Everything inside `@media (prefers-reduced-motion:no-preference)`. With motion
reduced, the first hero image is simply visible and nothing else moves.

## Narrow layouts

One breakpoint, at 820px. The three facts and the FAQ collapse to one column;
the split act recentres with the plate beneath the copy; the ledger collapses
its two figures into a single stacked column and drops the arrow between them.

The ledger header carries the same classes as its data cells so the labels stack
in the same order as the numbers they name. Without that, the headings landed in
raw grid order and labelled the wrong figures.

## What the page does not claim

The primary CTA reads **Install goguma**, not *Download for Mac*. There is no
notarised `.dmg` yet — the repo is private and the app is ad-hoc signed — so a
Download button would land the visitor on a `git clone`, which is a promise the
page cannot keep. The install block shows the three real commands, and they work.

The day a Developer ID build exists, both the nav button and the hero button
become *Download*, pointed at the release asset. That is the only copy on the
page that is waiting on something.

## Dock tiles

Three real app icons — Finder, Messages, Claude — supplied as `.icns` and extracted
with `iconutil`, plus goguma's sweet potato. An earlier pass used SF Symbols on
coloured tiles to avoid redistributing anyone's icon artwork; that was overruled,
and the files were dropped into the repo to be used.

The real icons arrive as finished macOS artwork: squircle, gradient and drop shadow
all baked in. So they get no tile of their own, only a box to centre them in. Their
squircle measures 80.2-81.2% of the canvas and sits vertically centred, so a 60px
image lands a 48.3px squircle.

goguma has no shipped icon yet, so its tile is drawn in CSS at 48px to land in
the same row: a pastel purple gradient (`#DCCBF2` to `#B694DD`), 11px radius —
macOS's corner at this size is 0.2237 x the tile — and its own shadow, since it has
none baked in. Purple rather than white, so it belongs to the page's palette while
the three system icons keep their own colours.

## The discovery scene

Replaced the hand-built Finder panel with a supplied desktop scene: a cursor
opens each place a schedule can hide and the jobs fly out into goguma's list.
Self contained, JS-free, one container query for scale, all motion inside
`prefers-reduced-motion` with the resting state as the finished state.

It is pasted in verbatim. The sources and job names are a sample of what someone
*could* have scheduled, not a readout of this machine, and that is deliberate:
the section sells discovery, so the interesting case is a machine with agent
work on it. An earlier pass here rewrote the scene to match a live
`import --dry-run` and was reverted. Illustrative content is the author's call,
not the page's.

The one thing the host page had to give up: `header{color:#fff}` was reaching
into the scene's own `<header>` and painting its heading white. All eight hero
rules are anchored to `header#top` now, so a bare element selector can never
again reach into a pasted-in section.

Two changes since, both from outside the pasted block. The cursor was 1.7cqw,
18.4px at full width, against the hero's 15px; it is 1.55cqw (16.7px) now,
keeping the artwork's 1.2706 aspect. And the loop is gated on visibility: the
scene runs ~10s, so starting it at page load means anyone scrolling down arrives
halfway through a story that only reads from the beginning. The scene already
ships a `data-wg-paused` hook, so an IntersectionObserver just drives it. The
attribute stays `false` in the markup, so with no JS the animation plays exactly
as it did and nothing about the section depends on the script.

Later it stopped being a centrepiece. Stacked and centred, it read as the page's
one big moment; it is now one of the split sections, desktop on the left and the
claim beside it on the right, matching the dial section's shape in mirror. Done
entirely from outside the pasted block: the scene's wrapper becomes a two-column
grid and `order` swaps its children, since the header comes first in the source.
Below 900px it goes back to one centred column, the way it was authored.

Half the width meant three follow-on fixes. The headline and lede were clamped
for centre stage, 76px and 23px, and are sized for a column now. The log block
grew from 26cqw to 36cqw so the paths stop ellipsising, which is as wide as it
can go before it runs under the panel at 52.5cqw. And the count column got a
50px floor, because 5.5cqw of a half-width stage is under 40px and "3 jobs"
wrapped.

## Section order

Hero, discovery, install, the sleep comparison, questions.

Two considerations pulled against each other. The value proposition is "awake
for the job, asleep for the rest", which argues for putting that section second
so the hero's claim is substantiated immediately. But three Mac desktops ran
back to back (the hero is one too), and the only non-desktop block on the page
is the install terminal, so it belongs between the two scenes.

The hero already states the value proposition in its own headline, which is what
breaks the tie: the second section does not have to repeat it. Discovery answers
the first objection ("do I have to configure this?"), install answers the second,
and the battery comparison lands last, where 0% against 94% reads as a closing
argument rather than a warm-up. It also puts a call to action at the midpoint
instead of only at the bottom.

The two scenes mirror each other now, scene-left then scene-right, with the dark
install block between them.

## One measure

Everything now sits on the same content box: 158 to 1282 at 1440, which is what
`.wrap` has always produced. Four things had drifted off it.

The sleep section had been widened to 1400 to make its scene bigger, which put
its left edge 110px outside every other section. It is back on `--max`, and the
scene came down from 940px to 760 to pay for it.

The discovery section was at 1240, now 1124, which lands on the same edges.

Its section also pads 32px horizontally where the page pads 28. That only shows
below the measure, where padding rather than max-width sets the edge, so it was
a 4px step that appeared on narrow screens only. Matched.

The sleep scene's own 32px inline padding pushed its panels inside their track,
so the two scenes' visible left edges differed by 32px even once their sections
agreed. Zeroed horizontally, which also makes the track arithmetic exact:
1080 x 0.704 = 760.

Checked at ten widths from 1600 down to 375. Section edges agree everywhere, and
the two scenes' panels start on the same pixel.

## FAQ order

Blockers before reassurances. The section now sits after the install commands,
so the questions that stop someone typing them come first: what it is, what it
will not touch, how it differs from `caffeinate`, then root, then privacy.
"Will it keep the machine awake" and "laptop in a bag" moved below those two,
because they reassure someone already inclined to install rather than remove a
reason not to. Linux stays last.

## The FAQ rows

Hover lights the whole disclosure, answer included when it is open, rather than
just the line of text. The fill sits on `details` with 18px of padding pulled
back out by an equal negative margin, so nothing inside moves; the question
still starts at exactly the same x whether the row is lit or not.

Opening slides rather than snaps. The CSS route for this is `::details-content`
with `interpolate-size: allow-keywords`, and it is genuinely supported here, but
on this page it measured stuck closed: `content-visibility` never left `hidden`
and the answer simply never appeared. It is animated with a WAAPI height
transition on a wrapper instead, driven off `.finished` rather than `onfinish`
so a fast second click cancels cleanly instead of slamming a reopened row shut.
Purely additive: `open` still decides visibility, so with the script stripped
the answers are fully readable.

Measured by driving the animation clock by hand, since headless freezes it:
opening runs 76 to 124 to 142 to 149px, closing 148 to 101 to 83 to 77.

## Copy


A pass for length and tells. Cut every em dash from visible text, cut three of
four uses of "actually", cut "your Mac" from the install output, and shortened
every FAQ answer. "Frequently asked questions" became "Questions". The retired
Finder panel's 27 CSS rules went with it.
