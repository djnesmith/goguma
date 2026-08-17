#!/usr/bin/env python3
"""Composites the landing page's desktop scenes around the real app renders.

The popover PNGs come out of `SurfaceRenderer` at `captureScale` against the
live daemon, so one AppKit point is `S` pixels here and every metric below is
written in points and multiplied. **`S` must equal `SurfaceRenderer.captureScale`**
— they were 4 and 3 for one build and the menu bar came out three quarters of
its right size against the popover. That matters: the first version of this script guessed pixel values,
and the result was a menu bar with the wrong height and the wrong type size,
which any Mac user reads as fake before they read a single word of the page.

The menu bar glyphs are genuine SF Symbols rendered by `build-symbols.swift`,
not arcs drawn with a path. Hand-drawn wifi and battery icons never match at any
size, and getting them subtly wrong is worse than leaving them out.

Three kinds of product image, deliberately:

  scene   the popover on a desktop under a menu bar — an establishing shot that
          answers "where does this live"
  plate   the panel alone, rounded and with an alpha channel, dropped onto the
          page's own ground — a detail shot that answers "what does it say"
  window  a document window with the title bar and traffic lights macOS draws
          but `SurfaceRenderer` cannot capture

The page alternates them. Two scenes back to back repeat the same wallpaper and
the same menu bar, and start to read as a template rather than as two moments.

Plates and windows carry no shadow of their own — CSS casts it, so it stays
crisp at any size and picks up the page's colour.
"""

import os
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageFont

HERE = os.path.dirname(os.path.abspath(__file__))
ASSETS = os.path.join(HERE, "assets")
SYMBOLS = os.path.join(HERE, "symbols")

S = 4  # render scale: must match SurfaceRenderer.captureScale

# macOS menu bar metrics, in points.
BAR_H = 24
BAR_TEXT = 13.5
BAR_PAD_L = 14
BAR_PAD_R = 13
MENU_GAP = 19
RIGHT_GAP = 17
POPOVER_RADIUS = 12
POPOVER_TOP = 6  # gap between the bar and the popover, as macOS leaves


# --------------------------------------------------------------------------
# wallpaper


def mesh(width, height, anchors, falloff=2.1):
    """A smooth mesh gradient: every pixel is an inverse-distance blend of the
    anchor colours. This is what modern macOS wallpapers look like, and it is
    the reason the earlier two-blurred-ellipses version read as a smudge — a
    Gaussian blur of two blobs has no colour structure, only mud."""
    ys, xs = np.mgrid[0:height, 0:width]
    xs = xs / width
    ys = ys / height
    num = np.zeros((height, width, 3), dtype=np.float64)
    den = np.zeros((height, width, 1), dtype=np.float64)
    for (ax, ay, colour, pull) in anchors:
        d2 = (xs - ax) ** 2 + (ys - ay) ** 2 + 1e-4
        w = pull / d2 ** (falloff / 2)
        w = w[..., None]
        num += w * np.array(colour, dtype=np.float64)
        den += w
    return num / den


def wallpaper(width, height, seed=7):
    """Deep aubergine with one small warm bloom in the far corner.

    An earlier version was much brighter and carried a large orange field, and
    on the page it read as a stock gradient rather than as somebody's desktop.
    Saturated colour is spent on the product — the sweet potato, the hold
    counter — so the wallpaper stays dark enough that those are the only things
    that glow."""
    field = mesh(
        width,
        height,
        [
            (-0.05, -0.05, (0x37, 0x1E, 0x54), 0.95),
            (0.30, 0.16, (0x4C, 0x28, 0x6E), 0.75),
            (0.92, -0.02, (0x1B, 0x0F, 0x29), 1.00),
            (-0.02, 0.55, (0x33, 0x18, 0x50), 0.90),
            (0.34, 0.72, (0x1C, 0x0E, 0x2B), 1.05),
            (0.70, 0.46, (0x55, 0x24, 0x58), 0.62),
            (1.04, 0.74, (0x84, 0x3C, 0x16), 0.38),
            (0.16, 1.04, (0x10, 0x09, 0x1A), 1.15),
            (0.72, 1.05, (0x1F, 0x0C, 0x21), 1.05),
        ],
    )

    # A faint diagonal sheen. Real wallpapers have direction; a pure radial
    # blend is inert, and the eye reads inert as synthetic.
    ys, xs = np.mgrid[0:height, 0:width]
    diag = ((xs / width) * 0.75 + (1 - ys / height) * 0.25)
    field += (np.sin(diag * 5.6) * 5.0)[..., None]

    # Grain, at the threshold of visibility. Without it the gradient bands
    # against the page's flat ground; with more of it, it looks like noise.
    rng = np.random.default_rng(seed)
    field += rng.normal(0, 2.0, (height, width, 1))

    return Image.fromarray(np.clip(field, 0, 255).astype(np.uint8), "RGB")


# --------------------------------------------------------------------------
# menu bar


SFNS = "/System/Library/Fonts/SFNS.ttf"


def font(pt, weight=400):
    """San Francisco at a given weight and the matching optical size.

    SFNS is a variable font whose axes are Width, Optical Size, GRAD and Weight,
    in that order. Selecting a face by `index` picks a named instance in a list
    hundreds long — the first version asked for index 1 expecting bold and got
    something far heavier and wider, which is why the menu bar's app name looked
    stamped on rather than typeset.

    Optical Size is not decoration. San Francisco has genuinely different
    letterforms for text and display sizes; leaving it at the 28 default while
    drawing at 13.5pt gives the loose spacing of a headline face at body size,
    and the menu bar reads subtly wrong without it being obvious why.
    """
    if os.path.exists(SFNS):
        try:
            f = ImageFont.truetype(SFNS, int(pt * S))
            f.set_variation_by_axes([100, max(17, min(96, pt)), 400, weight])
            return f
        except Exception:
            pass
    for path in ("/System/Library/Fonts/Helvetica.ttc",):
        if os.path.exists(path):
            try:
                return ImageFont.truetype(path, int(pt * S))
            except Exception:
                pass
    return ImageFont.load_default()


def symbol(name, height_pt, alpha=235):
    """An SF Symbol, tinted to menu bar white and scaled by height."""
    path = os.path.join(SYMBOLS, name + ".png")
    if not os.path.exists(path):
        return None
    img = Image.open(path).convert("RGBA")
    h = int(height_pt * S)
    img = img.resize((max(1, round(h * img.width / img.height)), h), Image.LANCZOS)
    a = img.getchannel("A").point(lambda v: int(v * alpha / 255))
    tint = Image.new("RGBA", img.size, (255, 255, 255, 255))
    tint.putalpha(a)
    return tint


def draw_menu_bar(base):
    """Returns the x-centre of the sweet potato glyph, so the popover can be
    anchored under its own status item the way the real one is."""
    width = base.width
    bar_h = BAR_H * S

    # The bar is a translucent dark blur over the wallpaper, not a flat fill.
    strip = base.crop((0, 0, width, bar_h)).filter(ImageFilter.GaussianBlur(18 * S))
    strip = Image.blend(strip, Image.new("RGB", strip.size, (14, 10, 18)), 0.72)
    base.paste(strip, (0, 0))

    d = ImageDraw.Draw(base)
    mid = bar_h / 2
    f_bold = font(BAR_TEXT, weight=590)  # the frontmost app's name only
    f = font(BAR_TEXT)

    # The status items are laid out first, right to left, because they are the
    # part that must be correct: the app menus are set dressing and can be
    # truncated, but Goguma's own glyph anchors the popover below it. Drawing
    # the menus first and hoping they stopped in time is what put "Window" and
    # the sweet potato on top of each other.
    right = width - BAR_PAD_R * S
    clock = "Fri 15:04"
    d.text((right, mid), clock, fill=(246, 243, 249), anchor="rm", font=f)
    right -= d.textlength(clock, font=f) + RIGHT_GAP * S

    for name, h in (("search", 15), ("control", 16), ("wifi", 14.5), ("battery", 12)):
        glyph = symbol(name, h)
        if not glyph:
            continue
        right -= glyph.width
        base.paste(glyph, (int(right), int(mid - glyph.height / 2)), glyph)
        right -= RIGHT_GAP * S

    # Goguma's own status item, in full colour among the monochrome — which
    # is exactly how it looks on a real menu bar, and is the point.
    potato = Image.open(os.path.join(ASSETS, "potato.png")).convert("RGBA")
    ph = int(16.5 * S)
    potato = potato.resize((max(1, round(ph * potato.width / potato.height)), ph), Image.LANCZOS)
    right -= potato.width
    base.paste(potato, (int(right), int(mid - potato.height / 2)), potato)
    centre = right + potato.width / 2

    # Menus fill whatever is left, and a menu that would come within 34pt of the
    # status items is simply not drawn — the same way macOS drops menus rather
    # than overlap them.
    limit = right - 34 * S
    x = BAR_PAD_L * S
    logo = symbol("apple", 15)
    if logo:
        base.paste(logo, (int(x), int(mid - logo.height / 2)), logo)
        x += logo.width + MENU_GAP * S

    for label, weight in (
        ("Finder", 590), ("File", 400), ("Edit", 400),
        ("View", 400), ("Go", 400), ("Window", 400), ("Help", 400),
    ):
        face = f_bold if weight > 400 else f
        advance = d.textlength(label, font=face)
        if x + advance > limit:
            break
        d.text((x, mid), label, fill=(255, 255, 255) if weight > 400 else (238, 234, 242),
               anchor="lm", font=face)
        x += advance + MENU_GAP * S

    return centre


# --------------------------------------------------------------------------
# popover


def save(img, name):
    """Writes the web asset as WebP.

    These are 2x composites with film grain over a gradient, which is close to
    the worst case for PNG: the grain defeats its predictors and every scene came
    out near 2MB, for a page total of 6.2MB. The same frame at WebP quality 90 is
    90KB — twenty times smaller — and the popover text inside it stays crisp,
    which is the only part that would show a lossy artefact.
    """
    stem = os.path.splitext(name)[0]
    if stem.startswith("plate"):
        # Lossless for the UI panel. It is the one image on the page a visitor
        # reads *text* out of, and lossy WebP softens 11px type no matter how
        # high the quality — 96 was still visibly worse than the source. The
        # photographic composites stay lossy, where nobody can tell.
        img.save(os.path.join(ASSETS, stem + ".webp"), "WEBP", lossless=True, method=6)
    else:
        img.save(os.path.join(ASSETS, stem + ".webp"), "WEBP", quality=90, method=6)


def rounded(img, radius):
    """macOS rounds its panels. Pasting a square-cornered render onto a desktop
    is the single most obvious tell that a screenshot was assembled."""
    mask = Image.new("L", img.size, 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, img.width - 1, img.height - 1], radius, fill=255)
    out = img.convert("RGBA")
    out.putalpha(mask)
    return out


def place(base, popover, x, y):
    """Two shadows: a wide ambient one for lift and a tight contact one for the
    edge. A single blur reads as a sticker."""
    canvas = base.convert("RGBA")
    silhouette = Image.new("RGBA", popover.size, (0, 0, 0, 255))
    silhouette.putalpha(popover.getchannel("A"))

    for blur, dy, opacity in ((54 * S / 2, 26, 0.42), (10 * S / 2, 7, 0.34)):
        layer = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
        layer.paste(silhouette, (x, y + dy))
        layer = layer.filter(ImageFilter.GaussianBlur(blur))
        a = layer.getchannel("A").point(lambda v: int(v * opacity))
        layer.putalpha(a)
        canvas = Image.alpha_composite(canvas, layer)

    canvas.alpha_composite(popover, (x, y))

    # The hairline macOS draws around a panel: light along the top, dark below.
    d = ImageDraw.Draw(canvas)
    d.rounded_rectangle(
        [x, y, x + popover.width - 1, y + popover.height - 1],
        POPOVER_RADIUS * S,
        outline=(255, 255, 255, 46),
        width=max(1, S // 2),
    )
    return canvas


def scene(source, out, frame_ratio=1.72, below=44, height=None):
    popover = rounded(
        Image.open(os.path.join(ASSETS, source)).convert("RGBA"), POPOVER_RADIUS * S
    )
    width = int(popover.width * frame_ratio)
    if height is None:
        height = BAR_H * S + POPOVER_TOP * S + popover.height + below * S

    base = wallpaper(width, height)
    centre = draw_menu_bar(base)

    x = int(min(max(centre - popover.width / 2, 22 * S), width - popover.width - 12 * S))
    y = BAR_H * S + POPOVER_TOP * S
    canvas = place(base, popover, x, y)

    save(canvas.convert("RGB"), out)
    return width, height


HERO = ("pop-idle.png", "pop-holding.png")


def plate(source, out):
    """The panel alone, corners rounded, alpha preserved."""
    img = rounded(
        Image.open(os.path.join(ASSETS, source)).convert("RGBA"), POPOVER_RADIUS * S
    )
    save(img, out)
    return img.size


WINDOW_RADIUS = 10
TITLEBAR_H = 28
LIGHTS = ((0xFF, 0x5F, 0x57), (0xFE, 0xBC, 0x2E), (0x28, 0xC8, 0x40))


def window(source, out):
    """A document window with its title bar and traffic lights.

    `SurfaceRenderer` captures the content view, so the chrome macOS draws
    around it is not in the PNG — and a window screenshot with no title bar does
    not read as a window, it reads as a rectangle someone cropped. The bar is
    filled from the capture's own top row so it matches whatever the app's
    background actually is rather than a guessed grey.
    """
    content = Image.open(os.path.join(ASSETS, source)).convert("RGBA")
    bar_h = TITLEBAR_H * S
    canvas = Image.new("RGBA", (content.width, content.height + bar_h), (0, 0, 0, 0))

    top_row = content.crop((0, 0, content.width, 1)).resize((content.width, bar_h))
    canvas.paste(top_row, (0, 0))
    canvas.paste(content, (0, bar_h))

    # macOS draws the close/minimise/zoom buttons at 12pt across on 20pt
    # centres, starting 20pt in. At half that they read as three specks and the
    # window looks like a diagram of a window.
    d = ImageDraw.Draw(canvas)
    r = 12 * S / 2
    cx = 20 * S
    cy = bar_h / 2
    for colour in LIGHTS:
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=colour + (255,))
        cx += 20 * S

    # No separator under the bar. `SurfaceRenderer` builds these windows with
    # `titlebarAppearsTransparent` and `fullSizeContentView`, which is a window
    # whose content runs under the chrome — macOS draws no line there, and one
    # drawn here reads as a seam between two pasted images.

    out_img = rounded(canvas, WINDOW_RADIUS * S)
    save(out_img, out)
    return out_img.size


def require(*names):
    """Fails with the list of missing renders rather than a stack trace.

    The inputs are `SurfaceRenderer` output taken against the live daemon and
    real jobs, so they are not reproducible from this repository alone — one of
    them (`pop-holding.png`) can only be captured while a job is genuinely
    holding, which is what `capture-holding.sh` waits for. Losing one should say
    so plainly and name it.
    """
    missing = [n for n in names if not os.path.exists(os.path.join(ASSETS, n))]
    if missing:
        raise SystemExit(
            "missing app renders: " + ", ".join(missing) + "\n"
            "  popover/jobs:  macos/.build/.../GogumaUI --render <surface> <path> light\n"
            "  expanded list: add  -popover.jobsExpanded YES\n"
            "  holding state: ./capture-holding.sh   (waits for a real hold)\n"
            "  sweet potato:  swift build-potato.swift assets/potato.png"
        )


def _pop_w(name):
    return Image.open(os.path.join(ASSETS, name)).width


def main():
    require("pop-idle.png", "potato.png")

    # The hero shows these at roughly 1124 CSS px wide, and the renders are 3x.
    # The frame width is chosen so the 340pt popover inside it lands back at
    # about 340 CSS px on screen: too narrow a frame and the panel is displayed
    # bigger than life and stops reading as a menu bar popover at all. The extra
    # desktop around it is also what makes the shot read as a screen.
    HERO_W = 3400
    hero_ratio = HERO_W / Image.open(os.path.join(ASSETS, "pop-idle.png")).width
    hero_frame = int(HERO_W / 2.55)  # matches the aspect the hero crops to


    made = [
        ("plate-idle.png",) + plate("pop-idle.png", "plate-idle.png"),
        # A tighter framing of the same desktop for the flow section. The hero
        # already shows the wide shot two hundred pixels above it, and running
        # the identical composition twice reads as a template rather than as two
        # moments — so this one crops in until the panel fills about half the
        # frame, with the menu bar still in it.
        # Derived from the popover's actual width, not a hardcoded one. The
        # literal 1020 here was the 3x width; when the capture went to 4x the
        # frame grew and the height did not, and the shot came out at the wrong
        # aspect with the menu bar squashed.
        ("scene-close.png",)
        + scene("pop-idle.png", "scene-close.png", 2.08,
                height=int(_pop_w("pop-idle.png") * 2.08 / 2.28)),
    ]
    for name, w, h in made:
        f = os.path.join(ASSETS, os.path.splitext(name)[0] + ".webp")
        print(f"{os.path.basename(f):26} {w}x{h}  {os.path.getsize(f)//1024}k")


if __name__ == "__main__":
    sys.exit(main())
