#!/usr/bin/env python3
"""Generates the hero sky.

The reference this is modelled on uses a photograph of a bright blue sky with
soft cumulus. Reaching for a CSS gradient instead is what makes a hero look
generated: a gradient has no structure, and the eye reads structure long before
it reads colour. So this builds actual cloud density with fractional Brownian
motion — several octaves of value noise summed at halving amplitude — and lights
it, rather than blurring a few coloured circles together.

It is the page's own pastel purple, not an invented one. A first version was a
saturated indigo-to-orange dawn, which was on-theme in the abstract and wrong in
practice: it belonged to no other part of the page and read as decoration bolted
on. The ramp here ends on exactly `--ground` (#F4F0F8), so the hero resolves
into the page rather than cutting against it, and the sky reads as the same
material as everything below it.
"""

import os

import numpy as np
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
ASSETS = os.path.join(HERE, "assets")

# 3840 wide because the hero is a full-bleed background: on a 1920 display at 2x
# it has to cover 3840 device pixels, and at 2560 it was being *upscaled* — 0.67x
# there, 0.89x at 1440. That is the softness, and no amount of compression
# quality fixes an image that is smaller than the surface it is stretched over.
W, H = 3840, 2400

# Must equal the page's `--ground`. The ramps below terminate on it exactly, which
# is what lets the hero and the closing section dissolve into the page instead of
# cutting against it. Change one and you must change the other.
GROUND = (0xEF, 0xE4, 0xF0)


def value_noise(shape, freq, rng):
    """One octave: a coarse random grid, smoothly resampled up to full size.

    Bilinear on its own leaves visible creases along the lattice, so the grid is
    resampled with PIL's bicubic filter, which is smooth enough at these octave
    counts that no lattice shows through the sum.
    """
    h, w = shape
    gh, gw = max(2, int(freq)), max(2, int(freq * w / h))
    grid = rng.random((gh, gw)).astype(np.float32)
    img = Image.fromarray((grid * 255).astype(np.uint8), "L").resize((w, h), Image.BICUBIC)
    return np.asarray(img, dtype=np.float32) / 255.0


def fbm(shape, rng, octaves=7, base=3.0, lacunarity=2.0, gain=0.5):
    total = np.zeros(shape, dtype=np.float32)
    amp, freq, norm = 1.0, base, 0.0
    for _ in range(octaves):
        total += amp * value_noise(shape, freq, rng)
        norm += amp
        amp *= gain
        freq *= lacunarity
    return total / norm


def smoothstep(edge0, edge1, x):
    t = np.clip((x - edge0) / (edge1 - edge0), 0.0, 1.0)
    return t * t * (3.0 - 2.0 * t)


def vertical_ramp(height, stops):
    """A vertical gradient from (position, rgb) stops, linear in between."""
    ys = np.linspace(0.0, 1.0, height, dtype=np.float32)
    out = np.zeros((height, 3), dtype=np.float32)
    for i in range(len(stops) - 1):
        (p0, c0), (p1, c1) = stops[i], stops[i + 1]
        m = (ys >= p0) & (ys <= p1)
        if not m.any():
            continue
        t = ((ys[m] - p0) / max(p1 - p0, 1e-6))[:, None]
        out[m] = np.array(c0, np.float32) * (1 - t) + np.array(c1, np.float32) * t
    return out


def build_dawn():
    rng = np.random.default_rng(11)
    shape = (H, W)
    ys = np.linspace(0.0, 1.0, H, dtype=np.float32)[:, None]

    # Deeper lavender overhead thinning to the page's own ground at the bottom
    # edge, which is what lets the hero dissolve into the rest of the page.
    sky = vertical_ramp(H, [
        (0.00, (0x9A, 0x70, 0xAA)),
        (0.26, (0xB4, 0x8F, 0xC1)),
        (0.52, (0xCE, 0xB6, 0xD8)),
        (0.78, (0xE8, 0xDD, 0xED)),
        (1.00, GROUND),
    ])[:, None, :].repeat(W, axis=1)

    # A soft high light off to the left, so the cloud field has a direction to
    # be lit from. Without one it reads as haze rather than as sky.
    xs = np.linspace(0.0, 1.0, W, dtype=np.float32)[None, :]
    sun = np.exp(-(((xs - 0.30) ** 2) / 0.20 + ((ys + 0.16) ** 2) / 0.13))
    sky += sun[..., None] * np.array([26, 22, 28], np.float32)

    # --- cloud density -------------------------------------------------------
    # Domain warping: fbm sampled through another fbm. This is what turns smooth
    # blobs into the sheared, wispy edges real cloud has.
    warp = fbm(shape, rng, octaves=5, base=2.5)
    density = fbm(shape, rng, octaves=8, base=2.2)
    density = np.clip(density + (warp - 0.5) * 0.42, 0.0, 1.0)

    # Cloud sits in the middle band: clear overhead so the headline has calm sky
    # behind it, and clearing again at the bottom so the product shot does not
    # land on texture.
    band = smoothstep(0.06, 0.40, ys) * (1.0 - smoothstep(0.72, 0.96, ys))

    # Two thresholds. The low wide one gives broad soft banks, the high narrow
    # one picks out the denser cores inside them. One alone produced isolated
    # blobs — a sky needs both the haze and the mass.
    banks = smoothstep(0.34, 0.70, density) * 0.62
    cores = smoothstep(0.60, 0.86, density)
    wisp = fbm(shape, rng, octaves=6, base=9.0)
    banks *= 0.65 + 0.55 * wisp
    cover = np.clip((banks + cores * 0.72) * band * 1.18, 0.0, 1.0)

    # --- lighting ------------------------------------------------------------
    # Light from above, so the tops of the banks catch it and the bodies stay a
    # cooler lavender. `np.gradient` along y is positive on a cloud's upper edge,
    # which is exactly where the highlight belongs.
    grad = np.gradient(cover.astype(np.float32), axis=0)
    edge = np.clip(grad * 30.0, 0.0, 1.0)
    # Ambient kept low on purpose. At 0.52 the cloud bodies washed out to almost
    # the same value as the sky and the whole field disappeared; the shape of a
    # cloud is in the difference between its lit top and its shaded body.
    ambient = 0.26 + 0.20 * (1.0 - cover)
    lit = np.clip(edge * 0.62 + ambient, 0.0, 1.0)

    shade = np.array([0xD1, 0xB8, 0xDC], np.float32)
    bright = np.array([0xFF, 0xFF, 0xFF], np.float32)
    cloud = shade[None, None, :] * (1 - lit[..., None]) + bright[None, None, :] * lit[..., None]

    out = sky * (1.0 - cover[..., None]) + cloud * cover[..., None]

    # Grain, at the threshold of visibility, so the smooth areas do not band.
    out += rng.normal(0.0, 1.7, (H, W, 1)).astype(np.float32)

    img = Image.fromarray(np.clip(out, 0, 255).astype(np.uint8), "RGB")
    img.save(os.path.join(ASSETS, "sky.webp"), "WEBP", quality=88, method=6)
    return img.size, os.path.getsize(os.path.join(ASSETS, "sky.webp"))


def build_night():
    """The night the page sinks into, behind the sleep statistic.

    Its top and bottom rows are `--ground`, ramping to black over the outer
    eighth, so the section arrives and leaves without a hard edge. A dark band
    butted straight against light paper reads as two pages stapled together; a
    ramp reads as dusk and dawn, which is the thing the page is actually about.
    """
    rng = np.random.default_rng(29)
    shape = (H, W)
    ys = np.linspace(0.0, 1.0, H, dtype=np.float32)[:, None]

    sky = vertical_ramp(H, [
        (0.00, GROUND),
        (0.055, (0x53, 0x34, 0x5F)),
        (0.115, (0x1F, 0x10, 0x29)),
        (0.36, (0x10, 0x08, 0x16)),
        (0.62, (0x0C, 0x06, 0x11)),
        (0.86, (0x1C, 0x0F, 0x25)),
        (0.94, (0x59, 0x38, 0x67)),
        (1.00, GROUND),
    ])[:, None, :].repeat(W, axis=1)

    # How dark it is here, used to keep stars and cloud out of the ramps.
    core = smoothstep(0.09, 0.22, ys) * (1.0 - smoothstep(0.84, 0.96, ys))

    # A moon: a small hard disc inside a soft halo. The halo on its own — which
    # is what a bare gaussian gives you — reads as a smudge on the lens rather
    # than as a light source. The disc is what makes it a moon.
    xs = np.linspace(0.0, 1.0, W, dtype=np.float32)[None, :]
    aspect = W / H
    r = np.sqrt(((xs - 0.80) * aspect) ** 2 + (ys - 0.30) ** 2)
    halo = np.exp(-(r ** 2) / 0.020)
    disc = 1.0 - smoothstep(0.0175, 0.0215, r)
    sky += (halo * core)[..., None] * np.array([70, 62, 96], np.float32)
    sky += (disc * core)[..., None] * np.array([215, 209, 232], np.float32)

    warp = fbm(shape, rng, octaves=5, base=2.5)
    density = np.clip(fbm(shape, rng, octaves=8, base=2.4) + (warp - 0.5) * 0.40, 0.0, 1.0)
    cover = np.clip(smoothstep(0.44, 0.80, density) * 0.55 * core, 0.0, 1.0)

    grad = np.gradient(cover.astype(np.float32), axis=0)
    lit = np.clip(np.clip(grad * 26.0, 0.0, 1.0) * 0.5 + 0.16, 0.0, 1.0)
    shade = np.array([0x18, 0x0C, 0x21], np.float32)
    bright = np.array([0x75, 0x57, 0x85], np.float32)
    cloud = shade[None, None, :] * (1 - lit[..., None]) + bright[None, None, :] * lit[..., None]
    out = sky * (1.0 - cover[..., None]) + cloud * cover[..., None]

    # Stars, in three sizes so the field has depth instead of looking sprayed.
    for cut, gain in ((0.99965, 1.0), (0.9993, 0.55), (0.998, 0.26)):
        pick = (rng.random(shape).astype(np.float32) > cut) * core * (1.0 - cover)
        out += pick[..., None] * np.array([205, 200, 222], np.float32) * gain

    out += rng.normal(0.0, 1.7, (H, W, 1)).astype(np.float32)
    img = Image.fromarray(np.clip(out, 0, 255).astype(np.uint8), "RGB")
    img.save(os.path.join(ASSETS, "sky-night.webp"), "WEBP", quality=88, method=6)
    return img.size, os.path.getsize(os.path.join(ASSETS, "sky-night.webp"))


def build_wall():
    """The desktop wallpaper behind the hero.

    Richer and darker than the page skies, because it is standing in for a real
    wallpaper rather than for atmosphere. The first attempt reused the dawn sky,
    which ramps to `--ground` so the hero could dissolve into the page — on a
    desktop that reads as a screen washing out to white, and it left the menu bar
    looking like a grey stripe with nothing to sit against.

    Only the last eighth ramps to `--ground` here, which is enough to keep the
    seam invisible without bleaching the whole lower half.
    """
    rng = np.random.default_rng(41)
    shape = (H, W)
    ys = np.linspace(0.0, 1.0, H, dtype=np.float32)[:, None]
    xs = np.linspace(0.0, 1.0, W, dtype=np.float32)[None, :]

    base = vertical_ramp(H, [
        (0.00, (0x44, 0x19, 0x5A)),
        (0.26, (0x61, 0x23, 0x7A)),
        (0.52, (0x82, 0x33, 0x8E)),
        (0.72, (0x97, 0x41, 0x81)),
        (0.86, (0xB1, 0x69, 0x8F)),
        (0.93, (0xD6, 0xB2, 0xD0)),
        (1.00, GROUND),
    ])[:, None, :].repeat(W, axis=1)

    # Flowing bands, the way a real wallpaper has direction. The angle field is
    # warped by fbm so the bands bend instead of running straight.
    warp = fbm(shape, rng, octaves=6, base=2.2)
    flow = np.sin((xs * 2.4 + ys * 3.1 + (warp - 0.5) * 5.6) * 3.0)
    ribbon = smoothstep(-0.15, 0.85, flow) * (1.0 - smoothstep(0.88, 1.0, ys))
    light = np.array([0xF0, 0xC4, 0xE6], np.float32)
    base += (ribbon * 0.30)[..., None] * (light - base) * 0.9

    # One warm bloom low-right, the ember the rest of the palette reserves.
    sun = np.exp(-(((xs - 0.88) ** 2) / 0.024 + ((ys - 0.78) ** 2) / 0.030))
    base += (sun * (1.0 - smoothstep(0.84, 0.98, ys)))[..., None] * np.array([88, 34, 4], np.float32)

    base += rng.normal(0.0, 1.8, (H, W, 1)).astype(np.float32)
    img = Image.fromarray(np.clip(base, 0, 255).astype(np.uint8), "RGB")
    img.save(os.path.join(ASSETS, "wall.webp"), "WEBP", quality=90, method=6)
    return img.size, os.path.getsize(os.path.join(ASSETS, "wall.webp"))


if __name__ == "__main__":
    for label, fn in (("sky.webp", build_dawn), ):
        size, nbytes = fn()
        print(f"{label:16} {size[0]}x{size[1]}  {nbytes // 1024}k")

    # The closing section is the dawn sky upside down: `--ground` at the top so
    # it arrives seamlessly out of the FAQ, deepening toward the foot of the
    # page. Read downward it is dusk, which is what "let it sleep" wants.
    dusk = Image.open(os.path.join(ASSETS, "sky.webp")).transpose(Image.FLIP_TOP_BOTTOM)
    dusk.save(os.path.join(ASSETS, "sky-dusk.webp"), "WEBP", quality=88, method=6)
    print(f"{'sky-dusk.webp':16} {dusk.width}x{dusk.height}  "
          f"{os.path.getsize(os.path.join(ASSETS, 'sky-dusk.webp')) // 1024}k")
